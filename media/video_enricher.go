package media

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

// VideoEnricher writes the file's own display candidate. It parses the
// filename before consulting TMDB and never changes its parent directory.
type VideoEnricher struct {
	catalog   *catalog.Catalog
	provider  MetadataProvider
	language  string
	threshold float64
}

func NewVideoEnricher(catalog *catalog.Catalog, provider MetadataProvider, language string) *VideoEnricher {
	if language == "" {
		language = "zh-CN"
	}
	return &VideoEnricher{catalog: catalog, provider: provider, language: language, threshold: .8}
}

func (p *VideoEnricher) Name() string { return "video_enrichment" }
func (p *VideoEnricher) Match(entry model.Entry) bool {
	return entry.Type == model.EntryFile && !strings.HasPrefix(entry.Name, "._") &&
		!strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") && movieVideoExtension(entry.Extension)
}

func (p *VideoEnricher) Process(ctx context.Context, entry model.Entry) error {
	libraryTypes, err := p.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return err
	}
	isTV := contains(libraryTypes, "tv")
	parsed := ParsedNameForEntry(entry, isTV)
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
	line1 := "电影"
	if kind == "episode" {
		line1 = "电视剧"
	}
	if parsed.Year != nil {
		line1 += " · " + strconv.Itoa(*parsed.Year)
	}
	data, err := json.Marshal(map[string]any{
		"season": parsed.Season, "episode": parsed.Episode, "release": parsed.Release,
	})
	if err != nil {
		return err
	}
	if _, err := p.catalog.SetMediaCandidate(ctx, entry.ID, "filename", catalog.MediaCandidate{
		Kind: kind, Title: parsed.Title, Year: parsed.Year, Line1: line1, Data: data,
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
	results, err := p.provider.Search(ctx, Query{Type: providerType, Title: parsed.Title, Year: parsed.Year, Language: p.language})
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
	_, err = p.catalog.SetMediaCandidate(ctx, entry.ID, "tmdb", catalog.MediaCandidate{
		Kind: kind, Title: match.Title, Year: match.Year, Summary: strings.TrimSpace(match.Overview), Data: encoded,
	})
	return err
}
