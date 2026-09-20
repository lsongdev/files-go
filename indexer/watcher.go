package indexer

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/storage"
)

var ErrWatchLimit = errors.New("filesystem watch limit reached")

func defaultMaxWatches() int {
	switch runtime.GOOS {
	case "linux":
		return 8192
	case "darwin":
		// kqueue consumes one descriptor per watched directory.
		return 128
	default:
		return 1024
	}
}

type Watcher struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
	indexer  *Indexer
	logger   *log.Logger
	watcher  *fsnotify.Watcher

	mu          sync.Mutex
	roots       map[string]string
	watched     map[string]bool
	timers      map[string]*time.Timer
	syncCh      chan watchedPath
	maxWatches  int
	limitWarned bool
	refreshing  bool
}

type watchedPath struct {
	storageID string
	absolute  string
	created   bool
}

func NewWatcher(catalog *catalog.Catalog, storages *storage.Registry, indexer *Indexer, logger *log.Logger) (*Watcher, error) {
	return NewWatcherWithLimit(catalog, storages, indexer, logger, 0)
}

func NewWatcherWithLimit(catalog *catalog.Catalog, storages *storage.Registry, indexer *Indexer, logger *log.Logger, maxWatches int) (*Watcher, error) {
	native, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Default()
	}
	if maxWatches <= 0 {
		maxWatches = defaultMaxWatches()
	}
	return &Watcher{catalog: catalog, storages: storages, indexer: indexer, logger: logger, watcher: native,
		roots: make(map[string]string), watched: make(map[string]bool), timers: make(map[string]*time.Timer),
		syncCh: make(chan watchedPath, 256), maxWatches: maxWatches}, nil
}

// Start attaches local storage roots immediately and fills recursive watches
// from cataloged directory paths in the background. The catalog, not a fresh
// disk walk, supplies the existing directory inventory.
func (w *Watcher) Start(ctx context.Context, storageIDs []string) error {
	for _, storageID := range storageIDs {
		backend, ok := w.storages.Get(storageID)
		if !ok {
			continue
		}
		native, ok := backend.(storage.NativeRooter)
		if !ok {
			continue
		}
		root := filepath.Clean(native.NativeRoot())
		w.roots[storageID] = root
		if err := w.add(root); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	// Library source roots are the highest-value watches and must be installed
	// before the bounded background expansion through cataloged directories.
	if libraries, err := w.catalog.Libraries(ctx); err == nil {
		for _, library := range libraries {
			for _, source := range library.Sources {
				if !w.indexer.inScanScope(source.StorageID, source.Path) {
					continue
				}
				root := w.roots[source.StorageID]
				if root == "" {
					continue
				}
				if err := w.add(filepath.Join(root, filepath.FromSlash(source.Path))); err != nil &&
					!errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrWatchLimit) {
					return err
				}
			}
		}
	}
	go w.run(ctx)
	return nil
}

func (w *Watcher) run(ctx context.Context) {
	refresh := time.NewTicker(time.Minute)
	defer refresh.Stop()
	defer w.close()
	go w.refreshCatalogDirectories(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
				w.debounce(event.Name, event.Op&fsnotify.Create != 0)
			}
		case err, ok := <-w.watcher.Errors:
			if ok && err != nil {
				w.logger.Printf("filesystem watcher: %v", err)
			}
		case item := <-w.syncCh:
			w.handle(ctx, item)
		case <-refresh.C:
			go w.refreshCatalogDirectories(ctx)
		}
	}
}

func (w *Watcher) debounce(absolute string, created bool) {
	storageID := w.storageFor(absolute)
	if storageID == "" {
		return
	}
	key := storageID + "\x00" + absolute
	w.mu.Lock()
	if timer := w.timers[key]; timer != nil {
		timer.Stop()
	}
	w.timers[key] = time.AfterFunc(350*time.Millisecond, func() {
		select {
		case w.syncCh <- watchedPath{storageID: storageID, absolute: absolute, created: created}:
		default:
			w.logger.Printf("filesystem watcher queue full; reconciliation will recover %s", absolute)
		}
	})
	w.mu.Unlock()
}

func (w *Watcher) handle(ctx context.Context, item watchedPath) {
	key := item.storageID + "\x00" + item.absolute
	w.mu.Lock()
	delete(w.timers, key)
	w.mu.Unlock()
	root := w.roots[item.storageID]
	relative, err := filepath.Rel(root, item.absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return
	}
	relative = filepath.ToSlash(relative)
	if !w.indexer.inScanScope(item.storageID, relative) {
		return
	}
	// A create event is often followed by a write event before the debounce
	// timer fires. Inspect the current object instead of relying on the final
	// event flag so copied directories always get their descendants indexed.
	if info, statErr := os.Stat(item.absolute); statErr == nil && info.IsDir() {
		w.syncCreatedTree(ctx, item.storageID, root, item.absolute)
		return
	}
	if _, err := w.indexer.SyncPath(ctx, item.storageID, relative); err != nil &&
		!errors.Is(err, ErrScanInProgress) && !errors.Is(err, context.Canceled) {
		w.logger.Printf("sync watched path %s: %v", relative, err)
	}
}

func (w *Watcher) syncCreatedTree(ctx context.Context, storageID, root, absolute string) {
	err := filepath.WalkDir(absolute, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if current != root && !w.indexer.inScanScope(storageID, filepath.ToSlash(relative)) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if err := w.add(current); err != nil && !errors.Is(err, ErrWatchLimit) {
				return err
			}
		}
		if current == root {
			return nil
		}
		_, err = w.indexer.SyncPath(ctx, storageID, filepath.ToSlash(relative))
		if errors.Is(err, ErrScanInProgress) {
			return nil
		}
		return err
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Printf("watch new directory %s: %v", absolute, err)
	}
}

func (w *Watcher) refreshCatalogDirectories(ctx context.Context) {
	w.mu.Lock()
	if w.refreshing {
		w.mu.Unlock()
		return
	}
	w.refreshing = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.refreshing = false
		w.mu.Unlock()
	}()
	for storageID, root := range w.roots {
		after := ""
		for {
			paths, err := w.catalog.AvailableDirectoryPaths(ctx, storageID, after, 2000)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					w.logger.Printf("load watch directories for %s: %v", storageID, err)
				}
				break
			}
			for _, value := range paths {
				if !w.indexer.inScanScope(storageID, value) {
					continue
				}
				if err := w.add(filepath.Join(root, filepath.FromSlash(value))); errors.Is(err, ErrWatchLimit) {
					w.warnWatchLimit()
					return
				} else if err != nil && !errors.Is(err, os.ErrNotExist) {
					w.logger.Printf("watch directory %s: %v", value, err)
				}
			}
			if len(paths) < 2000 {
				break
			}
			after = paths[len(paths)-1]
		}
	}
}

func (w *Watcher) add(directory string) error {
	directory = filepath.Clean(directory)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watched[directory] {
		return nil
	}
	if w.maxWatches > 0 && len(w.watched) >= w.maxWatches {
		return ErrWatchLimit
	}
	if err := w.watcher.Add(directory); err != nil {
		if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOSPC) {
			w.maxWatches = len(w.watched)
			return ErrWatchLimit
		}
		return err
	}
	w.watched[directory] = true
	return nil
}

func (w *Watcher) warnWatchLimit() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.limitWarned {
		return
	}
	w.limitWarned = true
	w.logger.Printf("filesystem watcher reached %d directories; scheduled reconciliation covers remaining paths", w.maxWatches)
}

func (w *Watcher) storageFor(absolute string) string {
	absolute = filepath.Clean(absolute)
	for storageID, root := range w.roots {
		relative, err := filepath.Rel(root, absolute)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return storageID
		}
	}
	return ""
}

func (w *Watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, timer := range w.timers {
		timer.Stop()
	}
	_ = w.watcher.Close()
}
