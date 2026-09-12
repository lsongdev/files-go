package media

import (
	"context"
	"encoding/json"
	"errors"
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
		{StorageID: "disk", Name: "S01E01.mkv", Path: "TV/Attack.On.Titan/S01/S01E01.mkv", Type: model.EntryFile, Extension: "mkv"},
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
	if err != nil || len(series) != 2 || series[0].Title != "Attack On Titan" || series[1].Title != "The Bear" || series[1].PrimaryEntryID != entries[1].ID {
		t.Fatalf("series = %#v, %v", series, err)
	}
	if err := cat.UnmatchEntry(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetMediaMatchSuppressed(ctx, entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaItemForEntry(ctx, entries[0].ID, "video"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("cataloger recreated suppressed movie: %v", err)
	}
}

func TestCatalogerBuildsArtistAlbumTrackHierarchy(t *testing.T) {
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
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	albums, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &root.ID, Name: "Discovery", Path: "Discovery", Type: model.EntryDirectory}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &albums[0].ID, Name: "01 - One More Time.flac", Path: "Discovery/01 - One More Time.flac", Type: model.EntryFile, Extension: "flac"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"music": map[string]any{"title": "One More Time", "artist": "Daft Punk", "album_artist": "Daft Punk", "album": "Discovery", "track": "1/14"}})
	if err := cat.UpsertMediaFile(ctx, model.MediaFile{EntryID: entries[0].ID, Kind: "audio", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	missing, err := cat.EntriesMissingMediaAssociation(ctx, "audio", "album", "", 100)
	if err != nil || len(missing) != 1 || missing[0].ID != entries[0].ID {
		t.Fatalf("missing album association = %#v, %v", missing, err)
	}
	if err := NewCataloger(cat).Process(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	track, err := cat.MediaItemForEntry(ctx, entries[0].ID, "audio")
	if err != nil || track.Type != "track" || track.ParentID == "" || track.IndexNumber == nil || *track.IndexNumber != 1 {
		t.Fatalf("track = %#v, %v", track, err)
	}
	album, err := cat.MediaItemForEntry(ctx, entries[0].ID, "album")
	if err != nil || album.Type != "album" || album.Title != "Discovery" || album.ParentID == "" {
		t.Fatalf("album = %#v, %v", album, err)
	}
	artist, err := cat.MediaItemForEntry(ctx, entries[0].ID, "artist")
	if err != nil || artist.Type != "artist" || artist.Title != "Daft Punk" {
		t.Fatalf("artist = %#v, %v", artist, err)
	}
	folderMedia, err := cat.MediaItemForDirectory(ctx, albums[0])
	if err != nil || folderMedia.ID != album.ID || folderMedia.PrimaryEntryID != entries[0].ID {
		t.Fatalf("folder media = %#v, %v", folderMedia, err)
	}
	missing, err = cat.EntriesMissingMediaAssociation(ctx, "audio", "album", "", 100)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing album association after cataloging = %#v, %v", missing, err)
	}
}
