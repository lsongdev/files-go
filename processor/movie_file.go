package processor

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/tmdb"
)

// MovieFile writes the file's own display candidate. It parses the
// filename before consulting TMDB and never changes its parent directory.
type MovieFile struct {
	catalog   *catalog.Catalog
	provider  tmdb.Provider
	language  string
	threshold float64
}

func NewMovieFile(catalog *catalog.Catalog, provider tmdb.Provider, language string) *MovieFile {
	if language == "" {
		language = "zh-CN"
	}
	return &MovieFile{catalog: catalog, provider: provider, language: language, threshold: .8}
}

func (p *MovieFile) Name() string { return "movie_metadata" }
func (p *MovieFile) Match(entry model.Entry) bool {
	return entry.Type == model.EntryFile && !strings.HasPrefix(entry.Name, "._") &&
		!strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") && movieVideoExtension(entry.Extension)
}

func (p *MovieFile) Process(ctx context.Context, entry model.Entry) error {
	libraryTypes, err := p.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return err
	}
	isTV := contains(libraryTypes, "tv")
	parsed := ParseMovieEntryName(entry, isTV)
	kind := ""
	if isTV && parsed.Season != nil && parsed.Episode != nil {
		kind = "episode"
	} else if contains(libraryTypes, "movies") {
		kind = "movie"
	}
	if kind == "" || parsed.Title == "" {
		_, err := p.catalog.ClearMediaCandidate(ctx, entry.ID, "filename")
		return err
	}
	line1, line2, line3 := MovieDisplay(kind, parsed, nil)
	data, err := json.Marshal(map[string]any{
		"season": parsed.Season, "episode": parsed.Episode, "release": parsed.Release,
	})
	if err != nil {
		return err
	}
	if _, err := p.catalog.SetMediaCandidate(ctx, entry.ID, "filename", catalog.MediaCandidate{
		Kind: kind, Title: parsed.Title, Year: parsed.Year, Line1: line1, Line2: line2, Line3: line3, Data: data,
	}); err != nil {
		return err
	}
	if p.provider == nil {
		return nil
	}
	current, err := p.catalog.MediaForEntry(ctx, entry.ID)
	if err != nil {
		return err
	}
	if current.MatchLocked {
		return nil
	}
	providerType := "movie"
	if kind == "episode" {
		providerType = "tv"
	}
	results, err := p.provider.Search(ctx, tmdb.Query{Type: providerType, Title: parsed.Title, Year: parsed.Year, Language: p.language})
	if err != nil {
		return err // Do not erase a previous match on a network error.
	}
	match, confidence, ok := bestCandidate(parsed, results)
	if !ok || confidence < p.threshold {
		_, err := p.catalog.ClearMediaCandidate(ctx, entry.ID, "tmdb")
		return err
	}
	if details, err := p.provider.Fetch(ctx, providerType, match.ID, p.language); err == nil {
		match = details
	}
	encoded, err := json.Marshal(match)
	if err != nil {
		return err
	}
	line1, line2, line3 = MovieDisplay(kind, parsed, &match)
	_, err = p.catalog.SetMediaCandidate(ctx, entry.ID, "tmdb", catalog.MediaCandidate{
		Kind: kind, Title: match.Title, Year: match.Year, Line1: line1, Line2: line2, Line3: line3,
		Summary: strings.TrimSpace(match.Overview), Data: encoded,
	})
	return err
}
