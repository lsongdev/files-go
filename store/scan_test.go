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
	if err := scanner.Scan(ctx, "r", false); err != nil {
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
	if err := scanner.Scan(ctx, "r", false); err != nil {
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
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st); n != 3 {
		t.Fatalf("after removing Movies: %d rows, want 3", n)
	}
}

func TestScanEmptyRoot(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	before := count(t, st)
	empty := func() {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
	// Emptied on the same filesystem as before: a real deletion.
	empty()
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatalf("emptied root: %v", err)
	}
	if n := count(t, st); n != 1 {
		t.Fatalf("emptied root kept %d rows", n)
	}

	// Empty on another filesystem than last time: an unmounted disk.
	write(t, filepath.Join(root, "a.txt"), "x")
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	before = count(t, st)
	st.CachePut(ctx, "root-device:r", []byte("12345"))
	empty()
	if err := scanner.Scan(ctx, "r", false); !errors.Is(err, ErrOffline) {
		t.Fatalf("scan of unmounted root = %v, want ErrOffline", err)
	}
	if status := scanner.Status()[0]; status.State != "offline" || count(t, st) != before {
		t.Fatalf("offline scan changed the index or state: %q", status.State)
	}
	// The user can confirm that it is really empty.
	if err := scanner.Scan(ctx, "r", true); err != nil || count(t, st) != 1 {
		t.Fatalf("forced scan: %v, %d rows", err, count(t, st))
	}

	os.RemoveAll(root)
	if err := scanner.Scan(ctx, "r", true); !errors.Is(err, ErrOffline) {
		t.Fatalf("scan of missing root = %v, want ErrOffline", err)
	}
}

func TestSyncDirAndAncestors(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	dir, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)")
	write(t, filepath.Join(root, "Movies", "Dune (2021)", "dune.srt"), "subs")
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

func TestSearchFollowsTitles(t *testing.T) {
	ctx := context.Background()
	st, scanner, _ := setup(t)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	movie, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	save := func(title string) {
		if err := st.SaveResults(ctx, []Result{{ID: movie.ID, Kind: "video", Title: title, Meta: []byte(`{}`), Version: 3}}); err != nil {
			t.Fatal(err)
		}
	}
	save("沙丘：第一部")
	if found, _ := st.Search(ctx, SearchOptions{Query: "沙丘"}); len(found) != 1 || found[0].ID != movie.ID {
		t.Fatalf("short title search = %+v", found)
	}
	if found, _ := st.Search(ctx, SearchOptions{Query: "第一部"}); len(found) != 1 {
		t.Fatalf("trigram title search = %+v", found)
	}
	save("Arrival")
	if found, _ := st.Search(ctx, SearchOptions{Query: "第一部"}); len(found) != 0 {
		t.Fatalf("stale title still indexed: %+v", found)
	}
	if found, _ := st.Search(ctx, SearchOptions{Query: "arrival"}); len(found) != 1 {
		t.Fatalf("new title not indexed: %+v", found)
	}
	if pending, _ := st.Pending(ctx, 3, Cursor{}, 100); len(pending) != 5 {
		t.Fatalf("pending after save = %d, want 5", len(pending))
	}
}

func TestVersionGrows(t *testing.T) {
	ctx := context.Background()
	st, _, _ := setup(t)
	a, _ := st.Version(ctx, "a")
	if again, _ := st.Version(ctx, "a"); again != a {
		t.Fatalf("same tag got %d then %d", a, again)
	}
	st.db.Exec(`UPDATE files SET meta_ver=?`, a)
	b, _ := st.Version(ctx, "b")
	back, _ := st.Version(ctx, "a")
	if b <= a || back <= b {
		t.Fatalf("versions must grow on every tag change: a=%d b=%d a'=%d", a, b, back)
	}
}

func TestInitRemovesUnconfiguredRoots(t *testing.T) {
	st, scanner, _ := setup(t)
	if err := scanner.Scan(context.Background(), "r", false); err != nil {
		t.Fatal(err)
	}
	empty := NewScanner(st, nil, nil, nil)
	if err := empty.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st); n != 0 {
		t.Fatalf("removed root left %d rows", n)
	}
	if err := scanner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st); n != 1 {
		t.Fatalf("recreated root has %d rows", n)
	}
}
