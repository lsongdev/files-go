package media

import (
	"context"
	"errors"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestBestCandidateUsesTitleAndYear(t *testing.T) {
	year := 2014
	wrong := 2016
	candidate, confidence, ok := bestCandidate(ParsedName{Title: "Interstellar", Year: &year}, []Candidate{{ID: "wrong", Title: "Interstellar", Year: &wrong}, {ID: "right", Title: "Interstellar", Year: &year}})
	if !ok || candidate.ID != "right" || confidence < .9 {
		t.Fatalf("candidate = %#v, %f, %v", candidate, confidence, ok)
	}
}

func TestBestCandidateMatchesLocalizedResultByOriginalTitle(t *testing.T) {
	year := 2014
	candidate, confidence, ok := bestCandidate(ParsedName{Title: "Interstellar", Year: &year}, []Candidate{{ID: "157336", Title: "星际穿越", OriginalTitle: "Interstellar", Year: &year}})
	if !ok || candidate.ID != "157336" || confidence < .9 {
		t.Fatalf("candidate = %#v, %f, %v", candidate, confidence, ok)
	}
}

type fakeProvider struct{}

func (fakeProvider) Search(_ context.Context, query Query) ([]Candidate, error) {
	year := 2014
	if query.Type == "tv" {
		year = 2023
		return []Candidate{{ID: "100", Type: "tv", Title: query.Title, Year: &year}}, nil
	}
	return []Candidate{{ID: "157336", Type: "movie", Title: query.Title, Year: &year}}, nil
}
func (fakeProvider) Fetch(_ context.Context, itemType, id, language string) (Candidate, error) {
	year := 2014
	if itemType == "tv" {
		year = 2023
	}
	return Candidate{ID: id, Type: itemType, Title: map[string]string{"movie": "Interstellar", "tv": "The Last of Us"}[itemType], Year: &year, Overview: language}, nil
}

func TestMatcherBuildsMovieAndTVHierarchy(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	for _, library := range []model.Library{{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}}, {ID: "tv", Name: "TV", Type: "tv", Sources: []model.LibrarySource{{StorageID: "disk", Path: "TV"}}}} {
		if err := cat.RegisterLibrary(ctx, library); err != nil {
			t.Fatal(err)
		}
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	directories, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Movies", Path: "Movies", Type: model.EntryDirectory}, {StorageID: "disk", Name: "TV", Path: "TV", Type: model.EntryDirectory}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	movieParent, tvParent := directories[0].ID, directories[1].ID
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &movieParent, Name: "Interstellar.2014.1080p.mkv", Path: "Movies/Interstellar.2014.1080p.mkv", Type: model.EntryFile, Extension: "mkv"}, {StorageID: "disk", ParentID: &tvParent, Name: "The.Last.of.Us.S01E03.2160p.mkv", Path: "TV/The.Last.of.Us.S01E03.2160p.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	matcher := NewMatcher(cat, fakeProvider{}, "en-US")
	for _, entry := range entries {
		if err := matcher.Process(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	movie, err := cat.MediaItemForEntry(ctx, entries[0].ID, "video")
	if err != nil || movie.Type != "movie" || movie.ExternalID != "tmdb:157336" {
		t.Fatalf("movie=%#v err=%v", movie, err)
	}
	candidates, err := matcher.Candidates(ctx, entries[0], "Corrected title")
	if err != nil || len(candidates) != 1 || candidates[0].Title != "Corrected title" {
		t.Fatalf("manual candidates=%#v err=%v", candidates, err)
	}
	manual, err := matcher.MatchCandidate(ctx, entries[0], "movie", "999")
	if err != nil || manual.ExternalID != "tmdb:999" || !manual.MatchLocked {
		t.Fatalf("manual movie=%#v err=%v", manual, err)
	}
	episode, err := cat.MediaItemForEntry(ctx, entries[1].ID, "video")
	if err != nil || episode.Type != "episode" || episode.IndexNumber == nil || *episode.IndexNumber != 3 {
		t.Fatalf("episode=%#v err=%v", episode, err)
	}
	season, err := cat.MediaItem(ctx, episode.ParentID)
	if err != nil || season.Type != "season" || season.IndexNumber == nil || *season.IndexNumber != 1 {
		t.Fatalf("season=%#v err=%v", season, err)
	}
	series, err := cat.MediaItem(ctx, season.ParentID)
	if err != nil || series.Type != "series" || series.ExternalID != "tmdb:100" {
		t.Fatalf("series=%#v err=%v", series, err)
	}
	if err := cat.UnmatchEntry(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetMediaMatchSuppressed(ctx, entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := matcher.Process(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaItemForEntry(ctx, entries[0].ID, "video"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("suppressed entry was rematched: %v", err)
	}
	manual, err = matcher.MatchCandidate(ctx, entries[0], "movie", "999")
	if err != nil || !manual.MatchLocked {
		t.Fatalf("manual rematch after suppression=%#v err=%v", manual, err)
	}
	if suppressed, err := cat.MediaMatchSuppressed(ctx, entries[0].ID); err != nil || suppressed {
		t.Fatalf("suppression after manual match=%v err=%v", suppressed, err)
	}
}
