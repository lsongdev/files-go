package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

type Matcher struct {
	catalog   *catalog.Catalog
	provider  MetadataProvider
	language  string
	threshold float64
}

var ErrInvalidManualMatch = errors.New("invalid manual media match")

func NewMatcher(catalog *catalog.Catalog, provider MetadataProvider, language string) *Matcher {
	if language == "" {
		language = "zh-CN"
	}
	return &Matcher{catalog: catalog, provider: provider, language: language, threshold: .8}
}

func (m *Matcher) Name() string { return "media_match" }
func (m *Matcher) Match(entry model.Entry) bool {
	if strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv", "rmvb":
		return !strings.HasPrefix(entry.Name, "._")
	default:
		return false
	}
}

func (m *Matcher) Candidates(ctx context.Context, entry model.Entry, title string) ([]Candidate, error) {
	if !m.Match(entry) {
		return []Candidate{}, nil
	}
	libraryTypes, err := m.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return nil, err
	}
	parsed := ParsedNameForEntry(entry, contains(libraryTypes, "tv"))
	if value := strings.TrimSpace(title); value != "" {
		parsed.Title = value
	}
	itemType := ""
	if parsed.Season != nil && parsed.Episode != nil && contains(libraryTypes, "tv") {
		itemType = "tv"
	} else if contains(libraryTypes, "movies") {
		itemType = "movie"
	}
	if itemType == "" || parsed.Title == "" {
		return []Candidate{}, nil
	}
	return m.provider.Search(ctx, Query{Type: itemType, Title: parsed.Title, Year: parsed.Year, Language: m.language})
}

func (m *Matcher) MatchCandidate(ctx context.Context, entry model.Entry, itemType, candidateID string) (*model.MediaItem, error) {
	if !m.Match(entry) {
		return nil, fmt.Errorf("%w: entry is not a supported video file", ErrInvalidManualMatch)
	}
	if itemType != "movie" && itemType != "tv" {
		return nil, fmt.Errorf("%w: type must be movie or tv", ErrInvalidManualMatch)
	}
	if strings.TrimSpace(candidateID) == "" {
		return nil, fmt.Errorf("%w: candidate ID is required", ErrInvalidManualMatch)
	}
	libraryTypes, err := m.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return nil, err
	}
	if (itemType == "movie" && !contains(libraryTypes, "movies")) || (itemType == "tv" && !contains(libraryTypes, "tv")) {
		return nil, fmt.Errorf("%w: type does not belong to this library", ErrInvalidManualMatch)
	}
	parsed := ParsedNameForEntry(entry, contains(libraryTypes, "tv"))
	if itemType == "tv" && (parsed.Season == nil || parsed.Episode == nil) {
		return nil, fmt.Errorf("%w: TV episode number could not be parsed from filename", ErrInvalidManualMatch)
	}
	candidate, err := m.provider.Fetch(ctx, itemType, candidateID, m.language)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	if err := m.catalog.SetMediaMatchSuppressed(ctx, entry.ID, false); err != nil {
		return nil, err
	}
	if err := m.catalog.UnmatchEntry(ctx, entry.ID); err != nil {
		return nil, err
	}
	if itemType == "movie" {
		err = m.matchMovie(ctx, entry, candidate, 1, metadata)
	} else {
		err = m.matchEpisode(ctx, entry, parsed, candidate, 1, metadata)
	}
	if err != nil {
		return nil, err
	}
	item, err := m.catalog.MediaItemForEntry(ctx, entry.ID, "video")
	if err != nil {
		return nil, err
	}
	if err := m.catalog.SetMediaMatchLocked(ctx, item.ID, true); err != nil {
		return nil, err
	}
	item.MatchLocked = true
	return item, nil
}

func (m *Matcher) Process(ctx context.Context, entry model.Entry) error {
	suppressed, err := m.catalog.MediaMatchSuppressed(ctx, entry.ID)
	if err != nil || suppressed {
		return err
	}
	if existing, err := m.catalog.MediaItemForEntry(ctx, entry.ID, "video"); err == nil {
		if existing.MatchLocked || existing.MatchSource == "tmdb" {
			return nil
		}
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	libraryTypes, err := m.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return err
	}
	parsed := ParsedNameForEntry(entry, contains(libraryTypes, "tv"))
	itemType := ""
	if parsed.Season != nil && parsed.Episode != nil && contains(libraryTypes, "tv") {
		itemType = "tv"
	}
	if itemType == "" && contains(libraryTypes, "movies") {
		itemType = "movie"
	}
	if itemType == "" || parsed.Title == "" {
		return nil
	}
	candidates, err := m.provider.Search(ctx, Query{Type: itemType, Title: parsed.Title, Year: parsed.Year, Language: m.language})
	if err != nil {
		return err
	}
	candidate, confidence, ok := bestCandidate(parsed, candidates)
	if !ok || confidence < m.threshold {
		return nil
	}
	details, err := m.provider.Fetch(ctx, itemType, candidate.ID, m.language)
	if err == nil {
		candidate = details
	}
	metadata, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	if itemType == "movie" {
		if err := m.catalog.UnmatchEntry(ctx, entry.ID); err != nil {
			return err
		}
		return m.matchMovie(ctx, entry, candidate, confidence, metadata)
	}
	if err := m.catalog.UnmatchEntry(ctx, entry.ID); err != nil {
		return err
	}
	return m.matchEpisode(ctx, entry, parsed, candidate, confidence, metadata)
}

func (m *Matcher) matchMovie(ctx context.Context, entry model.Entry, candidate Candidate, confidence float64, metadata []byte) error {
	externalID := "tmdb:" + candidate.ID
	item, err := m.catalog.MediaItemByExternalID(ctx, "movie", externalID)
	if errors.Is(err, catalog.ErrNotFound) {
		item, err = m.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: candidate.Title, SortTitle: sortTitle(candidate.Title), Year: candidate.Year, ExternalID: externalID, MatchSource: "tmdb", MatchConfidence: confidence, Metadata: metadata})
		if err != nil {
			item, err = m.catalog.MediaItemByExternalID(ctx, "movie", externalID)
		}
	}
	if err != nil {
		return err
	}
	if err := m.catalog.AssociateMediaFile(ctx, item.ID, entry.ID, "video"); err != nil {
		return err
	}
	return m.catalog.AssociateMediaLibraryFolder(ctx, entry, *item)
}

func (m *Matcher) matchEpisode(ctx context.Context, entry model.Entry, parsed ParsedName, candidate Candidate, confidence float64, metadata []byte) error {
	externalID := "tmdb:" + candidate.ID
	series, err := m.catalog.MediaItemByExternalID(ctx, "series", externalID)
	if errors.Is(err, catalog.ErrNotFound) {
		series, err = m.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "series", Title: candidate.Title, SortTitle: sortTitle(candidate.Title), Year: candidate.Year, ExternalID: externalID, MatchSource: "tmdb", MatchConfidence: confidence, Metadata: metadata})
		if err != nil {
			series, err = m.catalog.MediaItemByExternalID(ctx, "series", externalID)
		}
	}
	if err != nil {
		return err
	}
	season, err := m.catalog.MediaChild(ctx, series.ID, "season", *parsed.Season)
	if errors.Is(err, catalog.ErrNotFound) {
		title := fmt.Sprintf("%s — Season %d", series.Title, *parsed.Season)
		season, err = m.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "season", Title: title, SortTitle: fmt.Sprintf("%04d", *parsed.Season), ParentID: series.ID, IndexNumber: parsed.Season, MatchSource: "filename", MatchConfidence: confidence})
	}
	if err != nil {
		return err
	}
	episodeTitle := fmt.Sprintf("Episode %d", *parsed.Episode)
	episodeMetadata := metadata
	if provider, ok := m.provider.(EpisodeMetadataProvider); ok {
		if details, fetchErr := provider.FetchEpisode(ctx, candidate.ID, *parsed.Season, *parsed.Episode, m.language); fetchErr == nil {
			if strings.TrimSpace(details.Title) != "" {
				episodeTitle = details.Title
			}
			if encoded, encodeErr := json.Marshal(details); encodeErr == nil {
				episodeMetadata = encoded
			}
		}
	}
	episode, err := m.catalog.MediaChild(ctx, season.ID, "episode", *parsed.Episode)
	if errors.Is(err, catalog.ErrNotFound) {
		episode, err = m.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "episode", Title: episodeTitle, SortTitle: fmt.Sprintf("%04d", *parsed.Episode), ParentID: season.ID, IndexNumber: parsed.Episode, MatchSource: "tmdb", MatchConfidence: confidence, Metadata: episodeMetadata})
	} else if err == nil && episode.MatchSource != "tmdb" && episodeTitle != fmt.Sprintf("Episode %d", *parsed.Episode) {
		episode.Title = episodeTitle
		episode.MatchSource = "tmdb"
		episode.MatchConfidence = confidence
		episode.Metadata = episodeMetadata
		episode, err = m.catalog.UpsertMediaItem(ctx, *episode)
	}
	if err != nil {
		return err
	}
	for _, association := range []struct{ id, role string }{{series.ID, "series"}, {season.ID, "season"}, {episode.ID, "video"}} {
		if err := m.catalog.AssociateMediaFile(ctx, association.id, entry.ID, association.role); err != nil {
			return err
		}
	}
	return m.catalog.AssociateMediaLibraryFolder(ctx, entry, *series)
}

func bestCandidate(parsed ParsedName, candidates []Candidate) (Candidate, float64, bool) {
	type scored struct {
		candidate Candidate
		score     float64
	}
	items := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		score := titleScore(parsed.Title, candidate.Title)
		if originalScore := titleScore(parsed.Title, candidate.OriginalTitle); originalScore > score {
			score = originalScore
		}
		if parsed.Year != nil && candidate.Year != nil {
			if *parsed.Year == *candidate.Year {
				score += .2
			} else {
				score -= math.Min(.2, float64(abs(*parsed.Year-*candidate.Year))*.05)
			}
		}
		score += math.Min(.05, candidate.Popularity/1000)
		items = append(items, scored{candidate, score})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	if len(items) == 0 {
		return Candidate{}, 0, false
	}
	return items[0].candidate, items[0].score, true
}

var nonTitle = regexp.MustCompile(`[^\pL\pN]+`)

func normalizedTitle(value string) string {
	return nonTitle.ReplaceAllString(strings.ToLower(value), "")
}
func titleScore(left, right string) float64 {
	a, b := normalizedTitle(left), normalizedTitle(right)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return .8
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return .62
	}
	return 0
}
func sortTitle(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
