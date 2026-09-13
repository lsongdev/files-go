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

type recordingSink struct{ entries []model.Entry }

func (s *recordingSink) EnqueueEntries(_ context.Context, entries []model.Entry) error {
	s.entries = append(s.entries, entries...)
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
