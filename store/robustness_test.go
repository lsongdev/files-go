package store

import (
	"context"
	"errors"
	"github.com/lsongdev/files-go/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncompleteDirectoryReadPreservesIndex(t *testing.T) {
	ctx := context.Background()
	st, scanner, _ := setup(t)
	scanner.Scan(ctx, "r", false)
	before := count(t, st)
	f, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	choice := &Match{Provider: "tmdb", Type: "movie", ID: "1"}
	st.SetMatch(ctx, f, choice)
	read := scanner.readDir
	broken := errors.New("injected directory I/O error")
	scanner.readDir = func(f *os.File) ([]os.FileInfo, error) {
		entries, e := read(f)
		if filepath.Base(filepath.Clean(f.Name())) == "Movies" && len(entries) > 0 {
			return entries[:1], broken
		}
		return entries, e
	}
	if e := scanner.Scan(ctx, "r", false); !errors.Is(e, broken) {
		t.Fatalf("scan error: %v", e)
	}
	if count(t, st) != before {
		t.Fatal("incomplete read deleted index")
	}
	if got, e := st.Match(ctx, f); e != nil || got == nil || *got != *choice {
		t.Fatal("manual choice lost")
	}
	scanner.readDir = read
	if e := scanner.Scan(ctx, "r", false); e != nil {
		t.Fatal(e)
	}
}
func TestMountMarkerGuardsScansAndReads(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	scanner.roots[0].Marker = ".disk-id"
	write(t, filepath.Join(root, ".disk-id"), "disk A")
	if e := scanner.Scan(ctx, "r", false); e != nil {
		t.Fatal(e)
	}
	before := count(t, st)
	f, _ := st.ByPath(ctx, "r", "notes.txt")
	write(t, filepath.Join(root, ".disk-id"), "disk B")
	if e := scanner.Scan(ctx, "r", false); !errors.Is(e, ErrMountChanged) {
		t.Fatalf("mount change: %v", e)
	}
	if count(t, st) != before || scanner.Status()[0].State != "mount_changed" {
		t.Fatal("changed mount modified index")
	}
	if h, e := scanner.Open(ctx, f); e == nil {
		h.Close()
		t.Fatal("read from changed mount")
	}
	if e := scanner.Scan(ctx, "r", true); e != nil {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(root, ".disk-id"))
	if e := scanner.Scan(ctx, "r", true); !errors.Is(e, ErrMountChanged) {
		t.Fatalf("missing mandatory marker: %v", e)
	}
}
func TestSameMillisecondSameSizeEditInvalidates(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	p := filepath.Join(root, "notes.txt")
	base := time.Now().Truncate(time.Millisecond)
	first := base.Add(100 * time.Nanosecond)
	os.Chtimes(p, first, first)
	scanner.Scan(ctx, "r", false)
	before, _ := st.ByPath(ctx, "r", "notes.txt")
	st.db.Exec(`UPDATE files SET meta_ver=7`)
	os.WriteFile(p, []byte("world"), 0600)
	second := base.Add(700 * time.Nanosecond)
	os.Chtimes(p, second, second)
	info, _ := os.Stat(p)
	if info.ModTime().UnixNano() == before.MTimeNS {
		t.Skip("filesystem does not retain sub-millisecond mtimes")
	}
	if e := scanner.Scan(ctx, "r", false); e != nil {
		t.Fatal(e)
	}
	after, _ := st.Get(ctx, before.ID)
	if after.MTime != before.MTime || after.Revision <= before.Revision || after.MetaVer != 0 {
		t.Fatalf("missed nanosecond edit: before=%+v after=%+v", before, after)
	}
}
func TestRetryPersistsAndOldFailureCannotRestoreIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	name := filepath.Join(dir, "files.db")
	root := filepath.Join(dir, "root")
	write(t, filepath.Join(root, "a.mkv"), "x")
	st, e := Open(name)
	if e != nil {
		t.Fatal(e)
	}
	scanner := NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, func(string, int64) string { return "video" })
	scanner.Scan(ctx, "r", false)
	f, _ := st.ByPath(ctx, "r", "a.mkv")
	if e := st.Retry(ctx, f, errors.New("network")); e != nil {
		t.Fatal(e)
	}
	st.Close()
	st, e = Open(name)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	got, _ := st.Get(ctx, f.ID)
	if got.RetryCount != 1 || got.RetryAfter <= time.Now().UnixMilli() || got.MetaErr != "network" {
		t.Fatalf("retry lost: %+v", got)
	}
	pending, e := st.Pending(ctx, 1, Cursor{}, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range pending {
		if p.ID == f.ID {
			t.Fatal("delayed task dispatched")
		}
	}
	if e := st.Invalidate(ctx, f.ID); e != nil {
		t.Fatal(e)
	}
	st.Retry(ctx, f, errors.New("old failure"))
	got, _ = st.Get(ctx, f.ID)
	if got.RetryAfter != 0 || got.RetryCount != 0 || strings.Contains(got.MetaErr, "old") {
		t.Fatalf("stale failure overwrote reset: %+v", got)
	}
}
