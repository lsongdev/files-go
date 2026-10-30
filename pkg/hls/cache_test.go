package hls

import (
	"context"
	"github.com/lsongdev/files-go/pkg/source"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimelinePersistsAcrossManagersAndRejectsCorruption(t *testing.T) {
	ctx := context.Background()
	path := fixture(t, false)
	dir := t.TempDir()
	src := Source{File: source.Local(path), CopyVideo: true, OutputArgs: []string{"-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-c:a", "aac"}}
	cfg := Config{CacheDir: dir}
	first, e := New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	p, e := first.Open(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	first.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "timelines", "*.json"))
	if len(files) != 1 {
		t.Fatalf("timeline cache: %v", files)
	}
	second, e := New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	// A cached timeline must not invoke the probing routine. Use a canceled
	// process-slot budget while leaving the opener and source check operational.
	second.slots = make(chan struct{})
	deadline, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	replay, e := second.Open(deadline, src)
	if e != nil || replay.Duration != p.Duration {
		t.Fatalf("cached playback: %+v %v", replay, e)
	}
	second.Close()
	if e := os.WriteFile(files[0], []byte(`{"Version":2,"Segments":[{"Start":0,"Duration":-1}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	third, e := New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer third.Close()
	if _, e := third.Open(ctx, src); e != nil {
		t.Fatalf("corrupt cache was not rebuilt: %v", e)
	}
	info, _ := os.Stat(path)
	changed := info.ModTime().Add(1000000000)
	os.Chtimes(path, changed, changed)
	if _, e := third.Open(ctx, src); e != nil {
		t.Fatal(e)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "timelines", "*.json"))
	if len(files) != 2 {
		t.Fatal("changed source reused old timeline")
	}
}

func TestCacheLeaseAndAbandonedFragmentCleanup(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "hls-abandoned")
	os.Mkdir(orphan, 0700)
	os.WriteFile(filepath.Join(orphan, "partial"), []byte("partial"), 0600)
	first, e := New(context.Background(), Config{CacheDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(orphan); !os.IsNotExist(e) {
		t.Fatal("abandoned fragment directory was retained")
	}
	if second, e := New(context.Background(), Config{CacheDir: dir}); e == nil {
		second.Close()
		t.Fatal("concurrent cache owner was allowed")
	}
	first.Close()
	next, e := New(context.Background(), Config{CacheDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	next.Close()
}
