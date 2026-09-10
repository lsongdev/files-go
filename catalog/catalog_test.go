package catalog

import (
	"context"
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
