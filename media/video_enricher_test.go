package media

import (
	"context"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestVideoEnricherParsesFilenameBeforeTMDBAndOnlyWritesFile(t *testing.T) {
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
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}}); err != nil {
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
	library := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &root.ID, Name: "Movies", Path: "Movies", Type: model.EntryDirectory})
	folder := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &library.ID, Name: "Interstellar", Path: "Movies/Interstellar", Type: model.EntryDirectory})
	video := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &folder.ID, Name: "Interstellar.2014.1080p.BluRay.x264.mkv", Path: "Movies/Interstellar/Interstellar.2014.1080p.BluRay.x264.mkv", Type: model.EntryFile, Extension: "mkv"})
	processor := NewVideoEnricher(cat, fakeProvider{}, "zh-CN")
	if err := processor.Process(ctx, video); err != nil {
		t.Fatal(err)
	}
	item, err := cat.MediaForEntry(ctx, video.ID)
	if err != nil || item.Kind != "movie" || item.Title != "Interstellar" || item.Year == nil || *item.Year != 2014 || item.Line1 != "电影 · 2014" {
		t.Fatalf("video media = %#v, %v", item, err)
	}
	if _, err := cat.MediaForEntry(ctx, folder.ID); err == nil {
		t.Fatal("video enrichment was copied to its parent directory")
	}
}
