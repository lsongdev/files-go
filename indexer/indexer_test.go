package indexer

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

func TestScanGenerationRenameAndOffline(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "disk")
	if err := os.MkdirAll(filepath.Join(root, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	a, err := cat.EntryByPath(ctx, "disk", "folder/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	oldID := a.ID

	if err := os.Rename(filepath.Join(root, "folder", "a.txt"), filepath.Join(root, "folder", "c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "folder", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "d.txt"), []byte("d"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	c, err := cat.EntryByPath(ctx, "disk", "folder/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != oldID {
		t.Fatalf("renamed entry ID = %s, want %s", c.ID, oldID)
	}
	b, err := cat.EntryByPath(ctx, "disk", "folder/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b.Available {
		t.Fatal("deleted entry remained available")
	}
	if _, err := cat.EntryByPath(ctx, "disk", "folder/d.txt"); err != nil {
		t.Fatalf("new entry missing: %v", err)
	}

	if err := os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Scan(ctx, "disk"); err == nil {
		t.Fatal("offline scan unexpectedly succeeded")
	}
	c, err = cat.EntryByPath(ctx, "disk", "folder/c.txt")
	if err != nil {
		t.Fatalf("offline scan removed catalog entry: %v", err)
	}
	if c.Available {
		t.Fatal("offline entry should be reported unavailable")
	}
	state, err := cat.Storage(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "offline" {
		t.Fatalf("storage state = %q, want offline", state.State)
	}
}

func TestScanScopeIndexesOnlySelectedLibraries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"Projects/code.go", "Documents/book.epub", "Videos/Movies/movie.mkv", "Videos/TV Shows/show.mkv"} {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetScanScope("disk", []string{"Videos/Movies", "Videos/TV Shows"}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Videos/Movies/movie.mkv", "Videos/TV Shows/show.mkv"} {
		entry, err := cat.EntryByPath(ctx, "disk", path)
		if err != nil || !entry.Available {
			t.Fatalf("selected path %s = %#v, %v", path, entry, err)
		}
	}
	for _, path := range []string{"Projects/code.go", "Documents/book.epub"} {
		entry, err := cat.EntryByPath(ctx, "disk", path)
		if err != nil || entry.Available {
			t.Fatalf("excluded path %s = %#v, %v", path, entry, err)
		}
		if updated, err := idx.SyncPath(ctx, "disk", path); err != nil || updated != nil {
			t.Fatalf("excluded sync %s = %#v, %v", path, updated, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "Videos/Movies/movie.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Videos/Movies/new.mkv"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	reprocessed := &reprocessRecordingSink{}
	idx.SetEntrySink(reprocessed)
	if err := idx.ScanSubtree(ctx, "disk", "Videos/Movies"); err != nil {
		t.Fatal(err)
	}
	removed, err := cat.EntryByPath(ctx, "disk", "Videos/Movies/movie.mkv")
	if err != nil || removed.Available {
		t.Fatalf("removed movie = %#v, %v", removed, err)
	}
	created, err := cat.EntryByPath(ctx, "disk", "Videos/Movies/new.mkv")
	if err != nil || !created.Available {
		t.Fatalf("new movie = %#v, %v", created, err)
	}
	if len(reprocessed.entries) != 1 || reprocessed.entries[0].ID != created.ID {
		t.Fatalf("scoped enhancement reprocessed %#v", reprocessed.entries)
	}
	other, err := cat.EntryByPath(ctx, "disk", "Videos/TV Shows/show.mkv")
	if err != nil || !other.Available {
		t.Fatalf("unrelated library changed = %#v, %v", other, err)
	}
	if err := idx.ScanSubtree(ctx, "disk", "Projects"); !errors.Is(err, storage.ErrPathTraversal) {
		t.Fatalf("outside scope scan error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "Videos/Movies/new.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "Videos/Movies")); err != nil {
		t.Fatal(err)
	}
	if err := idx.ScanSubtree(ctx, "disk", "Videos/Movies"); err != nil {
		t.Fatal(err)
	}
	missing, err := cat.EntryByPath(ctx, "disk", "Videos/Movies")
	if err != nil || missing.Available {
		t.Fatalf("removed library root = %#v, %v", missing, err)
	}
	if err := idx.SetScanScope("disk", []string{""}); err != nil {
		t.Fatal(err)
	}
	if err := idx.ScanSubtree(ctx, "disk", ""); err != nil {
		t.Fatalf("root library scan: %v", err)
	}
	project, err := cat.EntryByPath(ctx, "disk", "Projects/code.go")
	if err != nil || !project.Available {
		t.Fatalf("root library did not include project: %#v, %v", project, err)
	}
}

func TestSyncPathAddsRenamesAndMarksExternalFilesUnavailable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "external.txt"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	created, err := idx.SyncPath(ctx, "disk", "external.txt")
	if err != nil || created == nil || !created.Available {
		t.Fatalf("created watched entry = %#v, %v", created, err)
	}
	originalID := created.ID
	if err := os.Rename(filepath.Join(root, "external.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.SyncPath(ctx, "disk", "external.txt"); err != nil {
		t.Fatal(err)
	}
	renamed, err := idx.SyncPath(ctx, "disk", "renamed.txt")
	if err != nil || renamed == nil || renamed.ID != originalID || renamed.Name != "renamed.txt" {
		t.Fatalf("renamed watched entry = %#v, %v; want ID %s", renamed, err, originalID)
	}
	if err := os.Remove(filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.SyncPath(ctx, "disk", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	removed, err := cat.Entry(ctx, originalID)
	if err != nil || removed.Available {
		t.Fatalf("removed watched entry = %#v, %v", removed, err)
	}
}

func TestRemovedArtworkReconcilesDirectoryInWatcherScopedAndFullScans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	folder := filepath.Join(root, "Movies", "Example")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(folder, "folder.jpg")
	if err := os.WriteFile(filename, []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", backend); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.SetScanScope("disk", []string{"Movies"}); err != nil {
		t.Fatal(err)
	}
	removed := 0
	idx.SetRemovedEntryReconciler(func(ctx context.Context, entry model.Entry) error {
		if entry.Name != "folder.jpg" {
			return nil
		}
		removed++
		_, err := cat.ClearMediaCandidate(ctx, *entry.ParentID, "local_artwork")
		return err
	})
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"watcher", "subtree", "full"} {
		if err := os.WriteFile(filename, []byte("image"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := idx.SyncPath(ctx, "disk", "Movies/Example/folder.jpg"); err != nil {
			t.Fatal(err)
		}
		entry, err := cat.EntryByPath(ctx, "disk", "Movies/Example/folder.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cat.SetMediaCandidate(ctx, *entry.ParentID, "local_artwork", catalog.MediaCandidate{Icon: "file:" + entry.ID}); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
		before := removed
		switch mode {
		case "watcher":
			_, err = idx.SyncPath(ctx, "disk", "Movies/Example/folder.jpg")
		case "subtree":
			err = idx.ScanSubtree(ctx, "disk", "Movies/Example")
		case "full":
			err = idx.Scan(ctx, "disk")
		}
		if err != nil {
			t.Fatalf("%s removal: %v", mode, err)
		}
		if removed != before+1 {
			t.Fatalf("%s removal invoked reconciler %d times, want 1", mode, removed-before)
		}
		if item, err := cat.MediaForEntry(ctx, *entry.ParentID); !errors.Is(err, catalog.ErrNotFound) || item != nil {
			t.Fatalf("%s retained stale artwork: %#v, %v", mode, item, err)
		}
	}
}

func TestWatcherObservesExternalCreateWithoutFullScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	watcher, err := NewWatcher(cat, registry, idx, log.New(os.Stderr, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Start(ctx, []string{"disk"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "noticed.txt"), []byte("noticed"), 0644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		entry, findErr := cat.EntryByPath(ctx, "disk", "noticed.txt")
		if findErr == nil && entry.Available {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not catalog external file: %#v, %v", entry, findErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWatcherStopsAtDescriptorBudget(t *testing.T) {
	watcher, err := NewWatcher(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.close()
	watcher.maxWatches = 1
	first := t.TempDir()
	second := t.TempDir()
	if err := watcher.add(first); err != nil {
		t.Fatal(err)
	}
	if err := watcher.add(second); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("second watch error = %v, want watch limit", err)
	}
}

type recordingSink struct{ entries []model.Entry }

func (s *recordingSink) EnqueueEntries(_ context.Context, entries []model.Entry) error {
	s.entries = append(s.entries, entries...)
	return nil
}

type reprocessRecordingSink struct{ entries []model.Entry }

func (s *reprocessRecordingSink) EnqueueEntries(_ context.Context, entries []model.Entry) error {
	s.entries = append(s.entries, entries...)
	return nil
}

func (s *reprocessRecordingSink) ReprocessEntry(_ context.Context, entry model.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func TestScanEnqueuesOnlyFilesForBackgroundProcessing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "one.txt"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	idx := New(cat, registry)
	idx.SetEntrySink(sink)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if len(sink.entries) != 1 || sink.entries[0].Name != "one.txt" || sink.entries[0].Type != model.EntryFile {
		t.Fatalf("enqueued entries = %#v", sink.entries)
	}
}

type interruptingStorage struct {
	storage.Storage
	failPath string
	failed   bool
	calls    map[string]int
}

func (s *interruptingStorage) ReadDir(ctx context.Context, path string) ([]storage.FileInfo, error) {
	s.calls[path]++
	if path == s.failPath && !s.failed {
		s.failed = true
		return nil, context.Canceled
	}
	return s.Storage.ReadDir(ctx, path)
}

func TestInterruptedScanResumesCompletedSubtrees(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, directory := range []string{"a-complete", "z-interrupted"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, directory, "file.txt"), []byte(directory), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	backend := &interruptingStorage{Storage: local, failPath: "z-interrupted", calls: make(map[string]int)}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", backend); err != nil {
		t.Fatal(err)
	}
	idx := New(cat, registry)
	if err := idx.Scan(ctx, "disk"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first scan error = %v, want context canceled", err)
	}
	interrupted, err := cat.Storage(ctx, "disk")
	if err != nil || interrupted.State != "interrupted" || interrupted.ScanEntries == 0 {
		t.Fatalf("interrupted storage = %#v, %v", interrupted, err)
	}
	firstEntries := interrupted.ScanEntries
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if backend.calls["a-complete"] != 1 {
		t.Fatalf("completed subtree read %d times, want once", backend.calls["a-complete"])
	}
	completed, err := cat.Storage(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "online" || completed.ScanEntries != 5 || completed.ScanEntries < firstEntries {
		t.Fatalf("completed storage = %#v; interrupted entries = %d", completed, firstEntries)
	}
}
