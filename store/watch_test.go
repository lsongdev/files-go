package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background reconciliation")
}

func runScanner(t *testing.T, s *Scanner, interval time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx, interval, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("scanner did not stop")
		}
	})
	eventually(t, func() bool { return !s.Status()[0].LastScan.IsZero() })
	if err := s.Status()[0].WatchError; err != "" {
		t.Fatalf("watcher: %s", err)
	}
}

func TestWatchReconcilesChanges(t *testing.T) {
	st, s, root := setup(t)
	runScanner(t, s, time.Hour)
	ctx := context.Background()
	exists := func(path string) bool { _, err := st.ByPath(ctx, "r", path); return err == nil }

	// Discover an entire prepopulated tree moved into a watched directory.
	incoming := filepath.Join(t.TempDir(), "incoming")
	write(t, filepath.Join(incoming, "nested", "video.mkv"), "first")
	if err := os.Rename(incoming, filepath.Join(root, "incoming")); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return exists("incoming/nested/video.mkv") })

	// Updating a file must work even though its parent directory mtime is unchanged.
	file := filepath.Join(root, "incoming", "nested", "video.mkv")
	dir := filepath.Dir(file)
	before, _ := os.Stat(dir)
	write(t, file, "a longer video")
	os.Chtimes(dir, before.ModTime(), before.ModTime())
	eventually(t, func() bool { f, _ := st.ByPath(ctx, "r", "incoming/nested/video.mkv"); return f.Size == 14 })

	// Existing subdirectories remain watched after a directory rename.
	if err := os.Rename(filepath.Join(root, "incoming"), filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return exists("renamed/nested/video.mkv") && !exists("incoming") })
	write(t, filepath.Join(root, "renamed", "nested", "new.txt"), "new")
	eventually(t, func() bool { return exists("renamed/nested/new.txt") })
	if err := os.RemoveAll(filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return !exists("renamed") })

	write(t, filepath.Join(root, ".hidden", "ignore.txt"), "ignore")
	write(t, filepath.Join(root, "node_modules", "ignore.txt"), "ignore")
	write(t, filepath.Join(root, "visible.txt"), "visible")
	eventually(t, func() bool { return exists("visible.txt") })
	if exists(".hidden") || exists("node_modules") {
		t.Fatal("excluded directories were indexed")
	}
}

func TestWatchCoalescesWrites(t *testing.T) {
	st, s, root := setup(t)
	runScanner(t, s, time.Hour)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		write(t, filepath.Join(root, "copy.mkv"), fmt.Sprintf("chunk %d", i))
		time.Sleep(300 * time.Millisecond)
		if _, err := st.ByPath(ctx, "r", "copy.mkv"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("file indexed before writes settled: %v", err)
		}
	}
	eventually(t, func() bool { f, err := st.ByPath(ctx, "r", "copy.mkv"); return err == nil && f.Size == 7 })
}

func TestWatchRootRecreated(t *testing.T) {
	st, s, root := setup(t)
	runScanner(t, s, time.Hour)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return s.Status()[0].State == "offline" })
	if _, err := st.ByPath(context.Background(), "r", "notes.txt"); err != nil {
		t.Fatal("offline root lost its index", err)
	}
	write(t, filepath.Join(root, "restored", "new.txt"), "restored")
	eventually(t, func() bool { _, err := st.ByPath(context.Background(), "r", "restored/new.txt"); return err == nil })
}

func TestPeriodicScanFallback(t *testing.T) {
	st, s, root := setup(t)
	runScanner(t, s, 100*time.Millisecond)
	// Polling should discover the file before the event debounce expires.
	write(t, filepath.Join(root, "periodic.txt"), "periodic")
	start := time.Now()
	eventually(t, func() bool { _, err := st.ByPath(context.Background(), "r", "periodic.txt"); return err == nil })
	if time.Since(start) >= watchQuiet {
		t.Fatal("periodic scan did not run independently of the event queue")
	}
}

func TestDirtyDirs(t *testing.T) {
	var d dirtyDirs
	now := time.Now()
	a := dirtyDir{"r", "a"}
	b := dirtyDir{"r", "b"}
	d.add(a, now)
	d.add(b, now)
	d.add(a, now.Add(time.Second))
	ready, full := d.take(now.Add(watchQuiet), true)
	if full || len(ready) != 1 || ready[0] != b {
		t.Fatalf("ready=%v full=%v", ready, full)
	}
	ready, _ = d.take(now.Add(watchQuiet+time.Second), true)
	if len(ready) != 1 || ready[0] != a {
		t.Fatalf("ready=%v", ready)
	}
	d.recover()
	if _, full = d.take(now, false); full {
		t.Fatal("recovery cooldown ignored")
	}
	if _, full = d.take(now, true); !full {
		t.Fatal("recovery request lost")
	}
	for i := 0; i < 4100; i++ {
		d.add(dirtyDir{"r", fmt.Sprint(i)}, now)
	}
	if len(d.dirs) > 4096 {
		t.Fatal("unbounded queue")
	}
	if _, full = d.take(now, true); !full {
		t.Fatal("queue overflow did not request recovery")
	}
}

func TestWatchEventPaths(t *testing.T) {
	_, s, root := setup(t)
	for _, path := range []string{filepath.Join(root, ".hidden", "x"), filepath.Join(root, "node_modules", "x"), root + "-other/file"} {
		if dirs := s.eventDirs(path); len(dirs) != 0 {
			t.Fatalf("unexpected events for %s: %v", path, dirs)
		}
	}
	dirs := s.eventDirs(filepath.Join(root, "Movies", "film.mkv"))
	if len(dirs) != 1 || dirs[0] != (dirtyDir{"r", "Movies"}) {
		t.Fatalf("dirs=%v", dirs)
	}
}
