package store

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watchQuiet = 2 * time.Second

// dirtyDirs coalesces events without blocking the OS event reader on scans or SQL.
// On overflow, one full reconciliation replaces the bounded incremental queue.
type dirtyDirs struct {
	mu   sync.Mutex
	dirs map[dirtyDir]time.Time
	full bool
}
type dirtyDir struct{ root, path string }

func (d *dirtyDirs) add(dir dirtyDir, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dirs == nil {
		d.dirs = make(map[dirtyDir]time.Time)
	}
	d.dirs[dir] = now.Add(watchQuiet)
	if len(d.dirs) > 4096 {
		clear(d.dirs)
		d.full = true
	}
}
func (d *dirtyDirs) recover() { d.mu.Lock(); d.full = true; d.mu.Unlock() }
func (d *dirtyDirs) take(now time.Time, allowFull bool) ([]dirtyDir, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	full := d.full && allowFull
	if full {
		d.full = false
	}
	var ready []dirtyDir
	for dir, due := range d.dirs {
		if !now.Before(due) {
			ready = append(ready, dir)
			delete(d.dirs, dir)
		}
	}
	return ready, full
}

// eventDir accepts only paths inside configured roots and outside excluded trees.
func (s *Scanner) eventDirs(name string) []dirtyDir {
	var dirs []dirtyDir
	for _, root := range s.roots {
		rel, err := filepath.Rel(filepath.Clean(root.Path), filepath.Clean(name))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		ignored := false
		if rel != "." {
			for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
				if s.excluded(part) {
					ignored = true
					break
				}
			}
		}
		if ignored {
			continue
		}
		dir := filepath.Dir(rel)
		if rel == "." || dir == "." {
			dir = ""
		}
		dirs = append(dirs, dirtyDir{root.ID, filepath.ToSlash(dir)})
	}
	return dirs
}

func (s *Scanner) syncChanged(ctx context.Context, changed dirtyDir) (result error) {
	if _, ok := s.Root(changed.root); !ok {
		return nil
	}
	defer func() {
		s.update(changed.root, func(st *RootStatus) {
			if st.State == "scanning" {
				return
			}
			st.State, st.Error = "idle", ""
			if errors.Is(result, ErrOffline) {
				st.State = "offline"
			} else if result != nil {
				st.State, st.Error = "error", result.Error()
			}
		})
	}()
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	root, _ := s.Root(changed.root)
	rel := changed.path
	for {
		dir, err := s.store.ByPath(ctx, root.ID, rel)
		if err == nil && dir.Dir {
			err = s.syncNew(ctx, root, dir)
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if rel == "" {
			return err
		}
		rel = filepath.ToSlash(filepath.Dir(rel))
		if rel == "." {
			rel = ""
		}
	}
}

// Run watches directories, reconciles quiet event batches, and periodically scans
// every root to recover missed events, unsupported mounts and offline disks.
// Run should be called once. Cancellation closes watches and joins the reader.
func (s *Scanner) Run(ctx context.Context, interval time.Duration, wake func()) {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	if wake == nil {
		wake = func() {}
	}
	var dirty dirtyDirs
	watcher, err := fsnotify.NewWatcher()
	var reader sync.WaitGroup
	if err != nil {
		log.Printf("file watcher unavailable; using periodic scans: %v", err)
		for _, root := range s.roots {
			s.update(root.ID, func(st *RootStatus) { st.WatchError = err.Error() })
		}
	} else {
		failures := make(map[string]string)
		watched := make(map[string]bool)
		var watchMu sync.Mutex
		watch := func(name string) {
			watchMu.Lock()
			defer watchMu.Unlock()
			previous := failures[name]
			err := watcher.Add(name)
			if err != nil {
				if failures[name] != err.Error() {
					log.Printf("watch %s: %v; periodic scanning remains enabled", name, err)
				}
				failures[name] = err.Error()
			} else {
				delete(failures, name)
				watched[name] = true
			}
			if err == nil && previous == "" {
				return
			}
			for _, root := range s.roots {
				message := ""
				for path, failure := range failures {
					rel, _ := filepath.Rel(root.Path, path)
					if path == filepath.Dir(root.Path) || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
						message = failure
						break
					}
				}
				s.update(root.ID, func(st *RootStatus) { st.WatchError = message })
			}
		}
		unwatch := func(name string) {
			watchMu.Lock()
			defer watchMu.Unlock()
			if !watched[name] {
				return
			} // ordinary file removals do not enumerate watches
			for path := range watched {
				if path == name || strings.HasPrefix(path, name+string(filepath.Separator)) {
					_ = watcher.Remove(path) // the OS may already have removed it
					delete(watched, path)
					delete(failures, path)
				}
			}
		}
		s.mu.Lock()
		s.watchDir = watch
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.watchDir = nil; s.mu.Unlock(); watcher.Close(); reader.Wait() }()
		reader.Add(1)
		go func() {
			defer reader.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-watcher.Events:
					if !ok {
						dirty.recover()
						return
					}
					// Drop the whole subtree so moved directories do not retain watches
					// on their old paths (or follow media moved outside a configured root).
					if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
						unwatch(event.Name)
					}
					// chmod is meaningful on Linux when an open file has been unlinked.
					for _, dir := range s.eventDirs(event.Name) {
						dirty.add(dir, time.Now())
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						dirty.recover()
						return
					}
					log.Printf("file watcher: %v; scheduling recovery scan", err)
					dirty.recover()
				}
			}
		}()
		// Watching the parents also detects a configured root being removed/recreated.
		for _, root := range s.roots {
			watch(filepath.Dir(root.Path))
		}
	}
	scan := func() { s.ScanAll(ctx); wake() }
	scan()
	periodic := time.NewTimer(interval)
	defer periodic.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var lastRecovery time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-periodic.C:
			scan()
			periodic.Reset(interval)
		case now := <-tick.C:
			dirs, full := dirty.take(now, now.Sub(lastRecovery) >= 30*time.Second)
			if full {
				lastRecovery = now
				scan()
			} else {
				for _, dir := range dirs {
					if err := s.syncChanged(ctx, dir); err != nil && ctx.Err() == nil {
						log.Printf("sync %s/%s: %v; scheduling recovery scan", dir.root, dir.path, err)
						dirty.recover()
					}
				}
				if len(dirs) > 0 {
					wake()
				}
			}
		}
	}
}
