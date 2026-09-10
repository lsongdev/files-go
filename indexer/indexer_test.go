package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
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
