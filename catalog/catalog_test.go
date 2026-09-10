package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestSearchTracksEntriesAndFiltersLibraries(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}}); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Movies", Path: "Movies", Type: model.EntryDirectory}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	parentID := root[0].ID
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &parentID, Name: "Interstellar.mkv", Path: "Movies/Interstellar.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}

	results, err := cat.Search(ctx, SearchOptions{Query: "inter", LibraryID: "movies", Type: model.EntryFile, Extension: ".MKV"})
	if err != nil || len(results) != 1 || results[0].ID != entries[0].ID {
		t.Fatalf("initial search = %#v, %v", results, err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "inter", LibraryID: "music"})
	if err != nil || len(results) != 0 {
		t.Fatalf("library-filtered search = %#v, %v", results, err)
	}

	renamed := entries[0]
	renamed.Name = "星际穿越.mkv"
	if _, err := cat.UpsertEntries(ctx, []model.Entry{renamed}, generation); err != nil {
		t.Fatal(err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "星际"})
	if err != nil || len(results) != 1 || results[0].Name != renamed.Name {
		t.Fatalf("renamed search = %#v, %v", results, err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "inter"})
	if err != nil || len(results) != 0 {
		t.Fatalf("stale name search = %#v, %v", results, err)
	}
}

func TestMediaFilesAndArtifactsRemainSeparateFromEntries(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "movie.mkv", Path: "movie.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	duration, width, height, bitrate := int64(123400), 1920, 1080, int64(8_000_000)
	media := model.MediaFile{EntryID: entries[0].ID, Kind: "video", DurationMS: &duration, Container: "matroska", Width: &width, Height: &height, VideoCodec: "h264", AudioCodec: "aac", Bitrate: &bitrate, Metadata: json.RawMessage(`{"streams":2}`)}
	if err := cat.UpsertMediaFile(ctx, media); err != nil {
		t.Fatal(err)
	}
	stored, err := cat.MediaFile(ctx, entries[0].ID)
	if err != nil || stored.Kind != "video" || stored.Width == nil || *stored.Width != width || string(stored.Metadata) != string(media.Metadata) {
		t.Fatalf("media file = %#v, %v", stored, err)
	}

	artifact, err := cat.UpsertArtifact(ctx, model.Artifact{EntryID: entries[0].ID, Type: "thumbnail", Variant: "medium", Key: "abc-medium", MIME: "image/jpeg", Size: 42})
	if err != nil {
		t.Fatal(err)
	}
	found, err := cat.ArtifactForEntry(ctx, entries[0].ID, "thumbnail", "medium")
	if err != nil || found.ID != artifact.ID || found.Key != "abc-medium" {
		t.Fatalf("artifact = %#v, %v", found, err)
	}
	previousCreated := found.CreatedAt
	artifact.Size = 84
	updated, err := cat.UpsertArtifact(ctx, *artifact)
	if err != nil || updated.ID != artifact.ID || updated.Size != 84 || !updated.CreatedAt.Equal(previousCreated) {
		t.Fatalf("updated artifact = %#v, %v", updated, err)
	}
}
