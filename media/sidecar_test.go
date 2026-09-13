package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

func TestSidecarAppliesLocalTVMetadataAndArtworkToFolder(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	files := map[string]string{
		"Show/tvshow.nfo":     `<tvshow><title>本地剧名</title><originaltitle>Local Show</originaltitle><plot>本地简介</plot><year>2024</year><genre>剧情</genre><uniqueid type="tmdb">123</uniqueid></tvshow>`,
		"Show/folder.jpg":     "poster",
		"Show/backdrop.jpg":   "backdrop",
		"Show/S01/S01E01.mkv": "video",
	}
	for name, contents := range files {
		filename := filepath.Join(rootPath, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	show := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &root.ID, Name: "Show", Path: "Show", Type: model.EntryDirectory})
	season := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &show.ID, Name: "S01", Path: "Show/S01", Type: model.EntryDirectory})
	nfo := insertSidecarEntry(t, cat, generation, sidecarFileEntry(t, rootPath, "disk", show.ID, "Show/tvshow.nfo"))
	poster := insertSidecarEntry(t, cat, generation, sidecarFileEntry(t, rootPath, "disk", show.ID, "Show/folder.jpg"))
	backdrop := insertSidecarEntry(t, cat, generation, sidecarFileEntry(t, rootPath, "disk", show.ID, "Show/backdrop.jpg"))
	video := insertSidecarEntry(t, cat, generation, sidecarFileEntry(t, rootPath, "disk", season.ID, "Show/S01/S01E01.mkv"))
	series, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "series", Title: "Show", ExternalID: "tmdb:123", MatchSource: "tmdb", Metadata: json.RawMessage(`{"posterPath":"/remote.jpg"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AssociateMediaFile(ctx, series.ID, video.ID, "series"); err != nil {
		t.Fatal(err)
	}
	stale, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "series", Title: "Wrong filename match", ExternalID: "filename:wrong", MatchSource: "filename"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AssociateMediaFile(ctx, stale.ID, video.ID, "series"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	processor := NewSidecar(cat, registry)
	if err := processor.Process(ctx, video); err != nil {
		t.Fatal(err)
	}
	got, err := cat.MediaItemForDirectory(ctx, show)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "本地剧名" || got.Year == nil || *got.Year != 2024 || got.MatchSource != "nfo" || got.PrimaryEntryID != poster.ID {
		t.Fatalf("folder media = %#v", got)
	}
	var metadata map[string]any
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["overview"] != "本地简介" || metadata["localPosterEntryId"] != poster.ID || metadata["localBackdropEntryId"] != backdrop.ID || metadata["nfoEntryId"] != nfo.ID {
		t.Fatalf("metadata = %#v", metadata)
	}
	summaries, err := cat.MediaSummariesForEntries(ctx, []string{show.ID})
	if err != nil {
		t.Fatal(err)
	}
	if summaries[show.ID].PrimaryEntryID != poster.ID || summaries[show.ID].Title != "本地剧名" {
		t.Fatalf("folder summary = %#v", summaries[show.ID])
	}
	if NewCataloger(cat).Match(poster) {
		t.Fatal("folder artwork must not be cataloged as a standalone photo")
	}
}

func insertSidecarEntry(t *testing.T, cat *catalog.Catalog, generation int64, entry model.Entry) model.Entry {
	t.Helper()
	items, err := cat.UpsertEntries(context.Background(), []model.Entry{entry}, generation)
	if err != nil {
		t.Fatal(err)
	}
	return items[0]
}

func sidecarFileEntry(t *testing.T, rootPath, storageID, parentID, path string) model.Entry {
	t.Helper()
	info, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return model.Entry{StorageID: storageID, ParentID: &parentID, Name: filepath.Base(path), Path: path,
		Type: model.EntryFile, Size: info.Size(), ModifiedAt: info.ModTime(), Extension: strings.TrimPrefix(filepath.Ext(path), ".")}
}
