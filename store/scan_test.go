package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
)

func write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Store, *Scanner, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	write(t, filepath.Join(root, "Movies", "Dune (2021)", "Dune.2021.mkv"), "video")
	write(t, filepath.Join(root, "Movies", "poster.jpg"), "jpg")
	write(t, filepath.Join(root, "notes.txt"), "hello")
	write(t, filepath.Join(root, ".hidden", "secret.txt"), "x")
	write(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "x")
	st, err := Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	classify := func(name string, size int64) string { return filepath.Ext(name) }
	scanner := NewScanner(st, []config.Root{{ID: "r", Name: "Root", Path: root}}, config.DefaultExclude, classify)
	if err := scanner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st, scanner, root
}

func count(t *testing.T, st *Store) int {
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScanReconcile(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	// root, Movies, Dune (2021), Dune.2021.mkv, poster.jpg, notes.txt; hidden and node_modules excluded
	if n := count(t, st); n != 6 {
		t.Fatalf("indexed %d rows, want 6", n)
	}
	movie, err := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	if err != nil || movie.Kind != ".mkv" || movie.Size != 5 {
		t.Fatalf("movie = %+v, %v", movie, err)
	}
	movies, _ := st.ByPath(ctx, "r", "Movies")
	if movies.Size != 2 {
		t.Fatalf("Movies child count = %d", movies.Size)
	}
	if results, _ := st.Search(ctx, SearchOptions{Query: "dune"}); len(results) != 2 {
		t.Fatalf("search dune = %d results", len(results))
	}
	if results, _ := st.Search(ctx, SearchOptions{Query: "no"}); len(results) != 1 {
		t.Fatalf("short search = %d results", len(results))
	}

	// Mark everything enriched, then change, add and remove files.
	if _, err := st.db.Exec(`UPDATE files SET meta_ver=7`); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	write(t, filepath.Join(root, "Movies", "Dune (2021)", "Dune.2021.mkv"), "video v2")
	os.Chtimes(filepath.Join(root, "Movies", "Dune (2021)", "Dune.2021.mkv"), future, future)
	write(t, filepath.Join(root, "new.mp3"), "audio")
	os.RemoveAll(filepath.Join(root, "Movies", "poster.jpg"))
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	changed, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	if changed.ID != movie.ID || changed.MetaVer != 0 || changed.Size != 8 {
		t.Fatalf("changed file = %+v", changed)
	}
	if _, err := st.ByPath(ctx, "r", "Movies/poster.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed file still indexed: %v", err)
	}
	if notes, _ := st.ByPath(ctx, "r", "notes.txt"); notes.MetaVer != 7 {
		t.Fatalf("unchanged file was invalidated: %+v", notes)
	}
	if pending, _ := st.Pending(ctx, 7, Cursor{}, 100); len(pending) == 0 {
		t.Fatal("expected pending rows after changes")
	}

	// Removing a directory drops its whole subtree.
	os.RemoveAll(filepath.Join(root, "Movies"))
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st); n != 3 {
		t.Fatalf("after removing Movies: %d rows, want 3", n)
	}
}

func TestScanOfflineKeepsIndex(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	before := count(t, st)
	// An empty mount point looks like an unmounted disk.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
	if err := scanner.Scan(ctx, "r"); !errors.Is(err, ErrOffline) {
		t.Fatalf("scan of empty root = %v, want ErrOffline", err)
	}
	os.RemoveAll(root)
	if err := scanner.Scan(ctx, "r"); !errors.Is(err, ErrOffline) {
		t.Fatalf("scan of missing root = %v, want ErrOffline", err)
	}
	if after := count(t, st); after != before {
		t.Fatalf("offline scan changed the index: %d -> %d", before, after)
	}
	if status := scanner.Status()[0]; status.State != "offline" {
		t.Fatalf("state = %q", status.State)
	}
}

func TestSyncDirAndAncestors(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	dir, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)")
	if scanner.Stale(dir) {
		t.Fatal("fresh directory reported stale")
	}
	write(t, filepath.Join(root, "Movies", "Dune (2021)", "dune.srt"), "subs")
	os.Chtimes(filepath.Join(root, "Movies", "Dune (2021)"), time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if !scanner.Stale(dir) {
		t.Fatal("changed directory not reported stale")
	}
	if err := scanner.SyncDir(ctx, dir); err != nil {
		t.Fatal(err)
	}
	children, _ := st.Children(ctx, dir.ID, 0, 10)
	if len(children) != 2 {
		t.Fatalf("children = %d", len(children))
	}
	ancestors, _ := st.Ancestors(ctx, children[0].ID)
	if len(ancestors) != 3 || ancestors[0].Path != "" || ancestors[2].Path != "Movies/Dune (2021)" {
		t.Fatalf("ancestors = %+v", ancestors)
	}
}
