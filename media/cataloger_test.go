package media

import (
	"context"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestCatalogerCreatesLocalMovieAndTVFallbacks(t *testing.T) {
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
	for _, library := range []model.Library{
		{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}},
		{ID: "tv", Name: "TV", Type: "tv", Sources: []model.LibrarySource{{StorageID: "disk", Path: "TV"}}},
	} {
		if err := cat.RegisterLibrary(ctx, library); err != nil {
			t.Fatal(err)
		}
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "Interstellar.2014.mkv", Path: "Movies/Interstellar.2014.mkv", Type: model.EntryFile, Extension: "mkv"},
		{StorageID: "disk", Name: "The.Bear.S02E03.mkv", Path: "TV/The.Bear.S02E03.mkv", Type: model.EntryFile, Extension: "mkv"},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	processor := NewCataloger(cat)
	for _, entry := range entries {
		if err := cat.UpsertMediaFile(ctx, model.MediaFile{EntryID: entry.ID, Kind: "video"}); err != nil {
			t.Fatal(err)
		}
		if err := processor.Process(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	movie, err := cat.MediaItemForEntry(ctx, entries[0].ID, "video")
	if err != nil || movie.Type != "movie" || movie.Title != "Interstellar" || movie.Year == nil || *movie.Year != 2014 {
		t.Fatalf("movie = %#v, %v", movie, err)
	}
	episode, err := cat.MediaItemForEntry(ctx, entries[1].ID, "video")
	if err != nil || episode.Type != "episode" || episode.ParentID == "" || episode.IndexNumber == nil || *episode.IndexNumber != 3 {
		t.Fatalf("episode = %#v, %v", episode, err)
	}
	series, err := cat.MediaItems(ctx, "series", "tv", 10)
	if err != nil || len(series) != 1 || series[0].Title != "The Bear" || series[0].PrimaryEntryID != entries[1].ID {
		t.Fatalf("series = %#v, %v", series, err)
	}
}
