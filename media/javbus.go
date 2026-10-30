package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/store"
)

// javbus is a JavBus scraper record saved next to a video as <code>.json or
// <video>.json. Like an NFO it is local input: it confirms the code, picks
// between catalog candidates sharing a code by release date, and describes
// works the catalog does not know.
type javbus struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	ReleaseDate string   `json:"release_date"`
	Length      string   `json:"length"` // "100min"
	Director    string   `json:"director"`
	Studio      string   `json:"studio"`
	Label       string   `json:"label"`
	Series      string   `json:"series"`
	Genre       []string `json:"genre"`
	Stars       []struct {
		Name string `json:"name"`
	} `json:"stars"`
}

// readJavBus finds the record for a video. Files that are not JavBus records,
// or describe another code, are ignored.
func readJavBus(ctx context.Context, st *store.Store, open Opener, f *store.File, code string) (*javbus, error) {
	if st == nil || open == nil {
		return nil, nil
	}
	names := []string{strings.TrimSuffix(f.Name, path.Ext(f.Name)) + ".json"}
	if code != "" {
		names = append(names, code+".json")
	}
	sidecar, err := st.ChildByName(ctx, f.Parent, names...)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file, err := open(ctx, sidecar)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return nil, err
	}
	var doc javbus
	if json.Unmarshal(data, &doc) != nil || javCode(doc.ID) == "" || code != "" && javCode(doc.ID) != code {
		return nil, nil
	}
	return &doc, nil
}

// apply describes the work from the record alone.
func (d *javbus) apply(m *Meta) {
	code := javCode(d.ID)
	m.Title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d.Title), d.ID))
	if m.Title == "" {
		m.Title = code
	}
	m.Year = leadingYear(d.ReleaseDate)
	if minutes, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(d.Length), "min")); err == nil && m.Duration == 0 {
		m.Duration = float64(minutes * 60)
	}
	var performers []string
	stars := map[string]bool{}
	for _, s := range d.Stars {
		performers = append(performers, s.Name)
		stars[s.Name] = true
	}
	m.Genres = nil
	for _, g := range d.Genre {
		if !stars[g] { // JavBus lists the performers among the genres
			m.Genres = append(m.Genres, g)
		}
	}
	m.Set("Performers", strings.Join(performers, ", "))
	m.Set("Studio", d.Studio)
	d.supplement(m)
}

// supplement adds what catalogs rarely carry to a catalog result.
func (d *javbus) supplement(m *Meta) {
	m.Set("Released", d.ReleaseDate)
	m.Set("Director", d.Director)
	m.Set("Label", d.Label)
	m.Set("Series", d.Series)
	if m.Info["Studio"] == "" {
		m.Set("Studio", d.Studio)
	}
}
