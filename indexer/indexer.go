package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type Indexer struct {
	catalog          *catalog.Catalog
	storages         *storage.Registry
	mu               sync.Mutex
	scanning         map[string]bool
	priority         map[string][]string
	scope            map[string][]string
	sink             EntrySink
	reconcileRemoved func(context.Context, model.Entry) error
}

type EntrySink interface {
	EnqueueEntries(context.Context, []model.Entry) error
}

type entryReprocessor interface {
	ReprocessEntry(context.Context, model.Entry) error
}

var ErrScanInProgress = errors.New("storage scan already in progress")

type scanProgress struct {
	entries, files, directories int64
	pending                     int64
	lastFlush                   time.Time
}

func New(catalog *catalog.Catalog, storages *storage.Registry) *Indexer {
	return &Indexer{catalog: catalog, storages: storages, scanning: make(map[string]bool), priority: make(map[string][]string), scope: make(map[string][]string)}
}

func (i *Indexer) SetEntrySink(sink EntrySink) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.sink = sink
}

// SetRemovedEntryReconciler rebuilds a parent directory's enhancement after
// an indexed sidecar, video, or season directory disappears.
func (i *Indexer) SetRemovedEntryReconciler(reconcile func(context.Context, model.Entry) error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.reconcileRemoved = reconcile
}

func (i *Indexer) reconcileRemovedEntry(ctx context.Context, entry model.Entry) error {
	if !affectsDirectoryMedia(entry) {
		return nil
	}
	i.mu.Lock()
	reconcile := i.reconcileRemoved
	i.mu.Unlock()
	if reconcile == nil {
		return nil
	}
	return reconcile(ctx, entry)
}

func affectsDirectoryMedia(entry model.Entry) bool {
	if entry.Type == model.EntryDirectory {
		return true
	}
	switch strings.ToLower(entry.Extension) {
	case "nfo", "jpg", "jpeg", "png", "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv", "rmvb":
		return true
	}
	return false
}

func (i *Indexer) EnqueueEntries(ctx context.Context, entries []model.Entry) error {
	i.mu.Lock()
	sink := i.sink
	i.mu.Unlock()
	if sink == nil {
		return nil
	}
	files := make([]model.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == model.EntryFile {
			files = append(files, entry)
		}
	}
	return sink.EnqueueEntries(ctx, files)
}

func (i *Indexer) ReprocessEntry(ctx context.Context, entry model.Entry) error {
	i.mu.Lock()
	sink := i.sink
	i.mu.Unlock()
	if sink == nil {
		return nil
	}
	if reprocessor, ok := sink.(entryReprocessor); ok {
		return reprocessor.ReprocessEntry(ctx, entry)
	}
	return sink.EnqueueEntries(ctx, []model.Entry{entry})
}

func (i *Indexer) IsScanning(storageID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.scanning[storageID]
}

// SyncPath applies one externally-observed filesystem path to the catalog.
// It touches only that path (and its parent lookup), preserving normal catalog
// browsing as a database-only operation.
func (i *Indexer) SyncPath(ctx context.Context, storageID, entryPath string) (*model.Entry, error) {
	if i.IsScanning(storageID) {
		return nil, ErrScanInProgress
	}
	entryPath = path.Clean(filepath.ToSlash(entryPath))
	if entryPath == "." {
		entryPath = ""
	}
	entryPath = strings.Trim(entryPath, "/")
	if entryPath == "" || entryPath == ".." || strings.HasPrefix(entryPath, "../") {
		return nil, storage.ErrPathTraversal
	}
	if !i.inScanScope(storageID, entryPath) {
		return nil, nil
	}
	backend, ok := i.storages.Get(storageID)
	if !ok {
		return nil, storage.ErrOffline
	}
	info, err := backend.Stat(ctx, entryPath)
	if errors.Is(err, storage.ErrNotFound) {
		existing, findErr := i.catalog.EntryByPath(ctx, storageID, entryPath)
		if errors.Is(findErr, catalog.ErrNotFound) {
			return nil, nil
		}
		if findErr != nil {
			return nil, findErr
		}
		if err := i.catalog.MarkEntryTreeUnavailable(ctx, *existing); err != nil && !errors.Is(err, catalog.ErrNotFound) {
			return nil, err
		}
		if err := i.reconcileRemovedEntry(ctx, *existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	parentPath := path.Dir(entryPath)
	if parentPath == "." {
		parentPath = ""
	}
	parent, err := i.catalog.EntryByPath(ctx, storageID, parentPath)
	if err != nil {
		return nil, fmt.Errorf("find watched parent %q: %w", parentPath, err)
	}
	if _, err := i.catalog.EntryByPath(ctx, storageID, entryPath); errors.Is(err, catalog.ErrNotFound) {
		if previous, identityErr := i.catalog.UnavailableEntryByIdentity(ctx, storageID, info.Device, info.Inode); identityErr == nil {
			if _, moveErr := i.catalog.MoveEntry(ctx, previous.ID, parent.ID, info.Name, info.Path); moveErr != nil {
				return nil, moveErr
			}
		} else if !errors.Is(identityErr, catalog.ErrNotFound) {
			return nil, identityErr
		}
	} else if err != nil {
		return nil, err
	}
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(info.Name)), ".")
	mimeType := ""
	if extension != "" {
		mimeType = mime.TypeByExtension("." + extension)
	}
	parentID := parent.ID
	updated, err := i.catalog.AddEntry(ctx, model.Entry{
		StorageID: storageID, ParentID: &parentID, Name: info.Name, Path: info.Path,
		Type: info.Type, Size: info.Size, ModifiedAt: info.ModifiedAt, Inode: info.Inode,
		Device: info.Device, MIME: mimeType, Extension: extension,
	})
	if err != nil {
		return nil, err
	}
	if updated.Type == model.EntryFile {
		if err := i.EnqueueEntries(ctx, []model.Entry{*updated}); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

// SetPriority makes configured library roots visible early during a large
// reconciliation scan without changing which entries are eventually indexed.
func (i *Indexer) SetPriority(storageID string, paths []string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	copyPaths := make([]string, 0, len(paths))
	for _, value := range paths {
		value = strings.Trim(strings.TrimPrefix(path.Clean(filepath.ToSlash(value)), "."), "/")
		if value != "" && value != ".." && !strings.HasPrefix(value, "../") {
			copyPaths = append(copyPaths, value)
		}
	}
	i.priority[storageID] = copyPaths
}

// SetScanScope limits a storage scan to selected library roots and their
// ancestors. Unlike priority, it excludes unrelated subtrees entirely.
// Passing an empty slice deliberately disables scanning this storage.
func (i *Indexer) SetScanScope(storageID string, paths []string) error {
	normalized := make([]string, 0, len(paths))
	for _, value := range paths {
		value = strings.Trim(path.Clean(filepath.ToSlash(value)), "/")
		if value == "." {
			value = ""
		}
		if value == ".." || strings.HasPrefix(value, "../") {
			return storage.ErrPathTraversal
		}
		normalized = append(normalized, value)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.scope[storageID] = normalized
	return nil
}

func (i *Indexer) inScanScope(storageID, value string) bool {
	i.mu.Lock()
	paths, scoped := i.scope[storageID]
	i.mu.Unlock()
	if !scoped {
		return true
	}
	for _, root := range paths {
		if root == "" {
			return true
		}
		if value == root || strings.HasPrefix(value, root+"/") || strings.HasPrefix(root, value+"/") {
			return true
		}
	}
	return false
}

func (i *Indexer) CanScanStorage(storageID string) bool {
	i.mu.Lock()
	paths, scoped := i.scope[storageID]
	i.mu.Unlock()
	return !scoped || len(paths) > 0
}

func (i *Indexer) Scan(ctx context.Context, storageID string) error {
	i.mu.Lock()
	if paths, scoped := i.scope[storageID]; scoped && len(paths) == 0 {
		i.mu.Unlock()
		return errors.New("storage has no selected library scan paths")
	}
	if i.scanning[storageID] {
		i.mu.Unlock()
		return ErrScanInProgress
	}
	i.scanning[storageID] = true
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		delete(i.scanning, storageID)
		i.mu.Unlock()
	}()
	backend, ok := i.storages.Get(storageID)
	if !ok {
		return fmt.Errorf("storage %q is not configured", storageID)
	}
	i.mu.Lock()
	selected, scoped := i.scope[storageID]
	selected = append([]string(nil), selected...)
	i.mu.Unlock()
	scopeKey := ""
	if scoped {
		sort.Strings(selected)
		encoded, err := json.Marshal(selected)
		if err != nil {
			return err
		}
		scopeKey = "libraries:" + string(encoded)
	}
	session, err := i.catalog.BeginScanSession(ctx, storageID, scopeKey)
	if err != nil {
		return err
	}
	generation := session.Generation
	root, err := i.catalog.EnsureRoot(ctx, storageID, generation)
	if err == nil {
		progress := &scanProgress{entries: session.Entries, files: session.Files, directories: session.Directories}
		if !session.Resumed {
			progress.entries = 1
			progress.directories = 1
		}
		err = i.flushProgress(ctx, storageID, progress, true)
		if err == nil {
			err = i.scanDirectory(ctx, backend, storageID, root, generation, progress)
		}
		flushCtx := ctx
		if err != nil {
			flushCtx = context.WithoutCancel(ctx)
		}
		if flushErr := i.flushProgress(flushCtx, storageID, progress, true); err == nil {
			err = flushErr
		}
	}
	if err != nil {
		state := "error"
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			state = "interrupted"
		} else if errors.Is(err, storage.ErrOffline) || errors.Is(err, storage.ErrNotFound) {
			state = "offline"
		}
		if failErr := i.catalog.FailScan(context.WithoutCancel(ctx), storageID, state, err.Error()); failErr != nil {
			return fmt.Errorf("scan failed: %v; update storage state: %w", err, failErr)
		}
		return err
	}
	removed, err := i.catalog.StaleDirectoryEvidence(ctx, storageID, generation)
	if err != nil {
		return err
	}
	if err := i.catalog.CompleteScan(ctx, storageID, generation); err != nil {
		return err
	}
	for _, entry := range removed {
		if err := i.reconcileRemovedEntry(ctx, entry); err != nil {
			return fmt.Errorf("reconcile removed entry %s: %w", entry.ID, err)
		}
	}
	return nil
}

func (i *Indexer) scanDirectory(ctx context.Context, backend storage.Storage, storageID string, parent *model.Entry, generation int64, progress *scanProgress) error {
	checkpoint, err := i.catalog.ScanCheckpoint(ctx, storageID, generation, parent.Path)
	if err != nil {
		return err
	}
	if checkpoint.Complete {
		return nil
	}
	infos, err := backend.ReadDir(ctx, parent.Path)
	if err != nil {
		return err
	}
	i.sortPriority(storageID, parent.Path, infos)
	const batchSize = 500
	entries := make([]model.Entry, 0, len(infos))
	for _, info := range infos {
		if !i.inScanScope(storageID, info.Path) {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(info.Name)), ".")
		mimeType := ""
		if ext != "" {
			mimeType = mime.TypeByExtension("." + ext)
		}
		parentID := parent.ID
		entries = append(entries, model.Entry{
			ID: uuid.Must(uuid.NewV7()).String(), StorageID: storageID, ParentID: &parentID,
			Name: info.Name, Path: info.Path, Type: info.Type, Size: info.Size,
			MIME: mimeType, Extension: ext, ModifiedAt: info.ModifiedAt,
			Inode: info.Inode, Device: info.Device, Available: true,
		})
		if !checkpoint.Listed {
			progress.entries++
			progress.pending++
			if info.Type == model.EntryDirectory {
				progress.directories++
			} else {
				progress.files++
			}
		}
	}
	if err := i.flushProgress(ctx, storageID, progress, false); err != nil {
		return err
	}
	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		updated, err := i.catalog.UpsertEntries(ctx, entries[start:end], generation)
		if err != nil {
			return err
		}
		copy(entries[start:end], updated)
		if err := i.EnqueueEntries(ctx, updated); err != nil {
			log.Printf("enqueue scanned entries: %v", err)
		}
	}
	if err := i.catalog.MarkScanDirectoryListed(ctx, storageID, generation, parent.Path); err != nil {
		return err
	}
	for index := range entries {
		if entries[index].Type == model.EntryDirectory {
			if err := i.scanDirectory(ctx, backend, storageID, &entries[index], generation, progress); err != nil {
				return err
			}
		}
	}
	return i.catalog.MarkScanDirectoryComplete(ctx, storageID, generation, parent.Path)
}

func (i *Indexer) flushProgress(ctx context.Context, storageID string, progress *scanProgress, force bool) error {
	now := time.Now()
	if !force && progress.pending < 1000 && !progress.lastFlush.IsZero() && now.Sub(progress.lastFlush) < time.Second {
		return nil
	}
	if err := i.catalog.UpdateScanProgress(ctx, storageID, progress.entries, progress.files, progress.directories); err != nil {
		return err
	}
	progress.pending = 0
	progress.lastFlush = now
	return nil
}

func (i *Indexer) sortPriority(storageID, parentPath string, infos []storage.FileInfo) {
	i.mu.Lock()
	paths := append([]string(nil), i.priority[storageID]...)
	i.mu.Unlock()
	if len(paths) == 0 {
		return
	}
	parentPath = strings.Trim(strings.TrimPrefix(path.Clean(filepath.ToSlash(parentPath)), "."), "/")
	rank := make(map[string]int)
	for index, value := range paths {
		value = strings.Trim(value, "/")
		child := ""
		if parentPath == "" {
			child = strings.SplitN(value, "/", 2)[0]
		} else if strings.HasPrefix(value, parentPath+"/") {
			child = strings.SplitN(strings.TrimPrefix(value, parentPath+"/"), "/", 2)[0]
		}
		if child != "" {
			rank[child] = index
		}
	}
	if len(rank) == 0 {
		return
	}
	sort.SliceStable(infos, func(left, right int) bool {
		leftRank, leftOK := rank[infos[left].Name]
		rightRank, rightOK := rank[infos[right].Name]
		if leftOK && rightOK {
			return leftRank < rightRank
		}
		if leftOK != rightOK {
			return leftOK
		}
		return infos[left].Name < infos[right].Name
	})
}
