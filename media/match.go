package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/lsongdev/files-go/store"
)

type retryState struct {
	attempts int
	revision int64
	after    time.Time
}

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
	return f, nil
}
