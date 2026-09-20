package catalog

import (
	"context"
	"testing"

	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestConfiguredLibrariesReplaceSourcesAndPruneRemovedLibraries(t *testing.T) {
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
	for _, library := range []model.Library{
		{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Old/Movies"}}},
		{ID: "documents", Name: "Documents", Type: "files", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Documents"}}},
	} {
		if err := cat.RegisterLibrary(ctx, library); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Videos/Movies"}}}); err != nil {
		t.Fatal(err)
	}
	if err := cat.PruneUnconfiguredLibraries(ctx, []string{"movies"}); err != nil {
		t.Fatal(err)
	}
	libraries, err := cat.Libraries(ctx)
	if err != nil || len(libraries) != 1 || libraries[0].ID != "movies" || len(libraries[0].Sources) != 1 || libraries[0].Sources[0].Path != "Videos/Movies" {
		t.Fatalf("configured libraries = %#v, %v", libraries, err)
	}
}

func TestScanProgressPersistsAndInterruptedScanDoesNotNeedInitialScan(t *testing.T) {
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
	needsScan, err := cat.NeedsInitialScan(ctx, "disk")
	if err != nil || !needsScan {
		t.Fatalf("empty storage needs scan = %v, %v", needsScan, err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.EnsureRoot(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateScanProgress(ctx, "disk", 42, 35, 7); err != nil {
		t.Fatal(err)
	}
	if err := cat.RecoverInterruptedScans(ctx); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if storage.State != "interrupted" || storage.ScanEntries != 42 || storage.ScanFiles != 35 || storage.ScanDirectories != 7 || storage.ScanStartedAt == nil || storage.ScanUpdatedAt == nil {
		t.Fatalf("recovered storage = %#v", storage)
	}
	needsScan, err = cat.NeedsInitialScan(ctx, "disk")
	if err != nil || needsScan {
		t.Fatalf("partial catalog needs initial scan = %v, %v", needsScan, err)
	}
}

func TestScanResumesCheckpointsAfterStorageGoesOffline(t *testing.T) {
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
	session, err := cat.BeginScanSession(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.MarkScanDirectoryComplete(ctx, "disk", session.Generation, "Movies"); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateScanProgress(ctx, "disk", 42, 35, 7); err != nil {
		t.Fatal(err)
	}
	if err := cat.FailScan(ctx, "disk", "offline", "mount missing"); err != nil {
		t.Fatal(err)
	}
	resumed, err := cat.BeginScanSession(ctx, "disk")
	if err != nil || !resumed.Resumed || resumed.Generation != session.Generation || resumed.Entries != 42 {
		t.Fatalf("resumed session = %#v, %v", resumed, err)
	}
}

func TestScanScopeChangeDiscardsOldCheckpoints(t *testing.T) {
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
	first, err := cat.BeginScanSession(ctx, "disk", "libraries:old")
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.MarkScanDirectoryComplete(ctx, "disk", first.Generation, "Projects"); err != nil {
		t.Fatal(err)
	}
	if err := cat.FailScan(ctx, "disk", "interrupted", "test"); err != nil {
		t.Fatal(err)
	}
	next, err := cat.BeginScanSession(ctx, "disk", "libraries:movies")
	if err != nil || next.Resumed || next.Entries != 0 {
		t.Fatalf("changed-scope session = %#v, %v", next, err)
	}
	checkpoint, err := cat.ScanCheckpoint(ctx, "disk", next.Generation, "Projects")
	if err != nil || checkpoint.Complete {
		t.Fatalf("stale checkpoint = %#v, %v", checkpoint, err)
	}
}

func TestCanceledScanIsRecoveredAsInterrupted(t *testing.T) {
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
	if _, err := cat.BeginScan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if err := cat.FailScan(ctx, "disk", "interrupted", context.Canceled.Error()); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil || storage.State != "interrupted" {
		t.Fatalf("storage = %#v, %v", storage, err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE storages SET state='error', scan_error='context canceled' WHERE id='disk'`); err != nil {
		t.Fatal(err)
	}
	if err := cat.RecoverInterruptedScans(ctx); err != nil {
		t.Fatal(err)
	}
	storage, err = cat.Storage(ctx, "disk")
	if err != nil || storage.State != "interrupted" {
		t.Fatalf("recovered storage = %#v, %v", storage, err)
	}
}

func TestRescanUsesExistingEntryCountAsProgressEstimate(t *testing.T) {
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
	generation, _ := cat.BeginScan(ctx, "disk")
	if _, err := cat.EnsureRoot(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "one", Path: "one", Type: model.EntryFile}}, generation); err != nil {
		t.Fatal(err)
	}
	if err := cat.CompleteScan(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.BeginScan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil || storage.ScanEstimate != 2 {
		t.Fatalf("scan estimate = %#v, %v", storage, err)
	}
}

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


func TestBrowseQueriesHideUnavailableEntries(t *testing.T) {
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
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	parentID := root.ID
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", ParentID: &parentID, Name: "visible.txt", Path: "visible.txt", Type: model.EntryFile},
		{StorageID: "disk", ParentID: &parentID, Name: "removed.txt", Path: "removed.txt", Type: model.EntryFile},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.MarkEntryTreeUnavailable(ctx, entries[1]); err != nil {
		t.Fatal(err)
	}
	children, err := cat.Children(ctx, root.ID, ListOptions{Limit: 10})
	if err != nil || len(children) != 1 || children[0].Name != "visible.txt" {
		t.Fatalf("children = %#v, %v", children, err)
	}
	results, err := cat.Search(ctx, SearchOptions{Query: "removed", Limit: 10})
	if err != nil || len(results) != 0 {
		t.Fatalf("search returned unavailable entries: %#v, %v", results, err)
	}
	stale, err := cat.EntryByPath(ctx, "disk", "removed.txt")
	if err != nil || stale.Available {
		t.Fatalf("internal stale lookup = %#v, %v", stale, err)
	}
}
