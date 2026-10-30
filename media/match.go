package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/lsongdev/files-go/store"
)

// Series carries show-level information separately from episode titles,
// summaries and stills, so directories can aggregate without catalog lookups.
type Series struct {
	Title    string   `json:"title"`
	Original string   `json:"original,omitempty"`
	Year     int      `json:"year,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Poster   string   `json:"poster,omitempty"`
	Backdrop string   `json:"backdrop,omitempty"`
	Rating   float64  `json:"rating,omitempty"`
	Genres   []string `json:"genres,omitempty"`
	Review   string   `json:"review,omitempty"`
}

func (m Meta) providerID(provider string) string {
	if m.Match == nil || m.Match.Provider != provider {
		return ""
	}
	return m.Match.Type + "/" + m.Match.ID
}

func (m *Meta) setMatch(provider, kind, id string) {
	m.Match = &store.Match{Provider: provider, Type: kind, ID: id}
}

var catalogID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateMatch keeps catalog rules out of the store and SDK packages.
func ValidateMatch(m store.Match) error {
	switch m.Provider {
	case "tmdb":
		n, err := strconv.Atoi(m.ID)
		if (m.Type == "movie" || m.Type == "tv") && err == nil && n > 0 && strconv.Itoa(n) == m.ID {
			return nil
		}
	case "theporndb":
		if m.Type == "jav" && m.ID != "" && len(m.ID) <= 128 && catalogID.MatchString(m.ID) {
			return nil
		}
	}
	return fmt.Errorf("invalid catalog match")
}

// MatchScope redirects a shared work's manual choice to its directory. Mixed
// collections and loose files keep file-specific choices.
func (p *Pipeline) MatchScope(ctx context.Context, f store.File) (store.File, error) {
	if owner, err := p.store.MatchOwner(ctx, f); err == nil {
		return owner, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return f, err
	}
	if f.Dir {
		return f, nil
	}
	var m Meta
	_ = json.Unmarshal(f.Meta, &m)
	ancestors, err := p.store.Ancestors(ctx, f.ID)
	if err != nil {
		return f, err
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		a := ancestors[i]
		var am Meta
		_ = json.Unmarshal(a.Meta, &am)
		if (am.Kind == "show" || am.Kind == "movie" && a.ID == f.Parent) &&
			sameWork([]member{{file: f, meta: m}, {file: a, meta: am}}) {
			return a, nil
		}
	}
	// A movie awaiting review does not identify its folder, but choosing it
	// for one part applies to every part of the same work.
	if len(ancestors) > 0 && m.Kind == "movie" {
		parent := ancestors[len(ancestors)-1]
		if parts, err := p.parts(ctx, parent); err != nil {
			return f, err
		} else if len(parts) > 1 && sameWork(parts) {
			return parent, nil
		}
	}
	return f, nil
}

// SharedWork reports whether a choice may be made for a whole directory: an
// identified work, or the parts of one movie still awaiting review.
func (p *Pipeline) SharedWork(ctx context.Context, dir store.File) (bool, error) {
	var m Meta
	_ = json.Unmarshal(dir.Meta, &m)
	if m.Kind == "movie" || m.Kind == "show" || m.Kind == "season" {
		return true, nil
	}
	parts, err := p.parts(ctx, dir)
	return err == nil && len(parts) > 0 && sameWork(parts), err
}

// parts returns a directory's main videos when all of them are movies and it
// has no media subfolders; otherwise none.
func (p *Pipeline) parts(ctx context.Context, dir store.File) ([]member, error) {
	children, err := p.store.Children(ctx, dir.ID, 0, -1)
	if err != nil {
		return nil, err
	}
	var parts []member
	for _, c := range children {
		var cm Meta
		_ = json.Unmarshal(c.Meta, &cm)
		switch {
		case c.Dir && cm.Files > 0:
			return nil, nil
		case c.Kind == "video" && !isExtra(c.Name):
			if cm.Kind != "movie" {
				return nil, nil
			}
			parts = append(parts, member{c, cm})
		}
	}
	return parts, nil
}
