package indexer

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type Indexer struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
	mu       sync.Mutex
	scanning map[string]bool
	priority map[string][]string
}

var ErrScanInProgress = errors.New("storage scan already in progress")

func New(catalog *catalog.Catalog, storages *storage.Registry) *Indexer {
	return &Indexer{catalog: catalog, storages: storages, scanning: make(map[string]bool), priority: make(map[string][]string)}
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

func (i *Indexer) Scan(ctx context.Context, storageID string) error {
	i.mu.Lock()
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
	generation, err := i.catalog.BeginScan(ctx, storageID)
	if err != nil {
		return err
	}
	root, err := i.catalog.EnsureRoot(ctx, storageID, generation)
	if err == nil {
		err = i.scanDirectory(ctx, backend, storageID, root, generation)
	}
	if err != nil {
		state := "error"
		if errors.Is(err, storage.ErrOffline) || errors.Is(err, storage.ErrNotFound) {
			state = "offline"
		}
		if failErr := i.catalog.FailScan(context.WithoutCancel(ctx), storageID, state); failErr != nil {
			return fmt.Errorf("scan failed: %v; update storage state: %w", err, failErr)
		}
		return err
	}
	return i.catalog.CompleteScan(ctx, storageID, generation)
}

func (i *Indexer) scanDirectory(ctx context.Context, backend storage.Storage, storageID string, parent *model.Entry, generation int64) error {
	infos, err := backend.ReadDir(ctx, parent.Path)
	if err != nil {
		return err
	}
	i.sortPriority(storageID, parent.Path, infos)
	const batchSize = 500
	entries := make([]model.Entry, 0, len(infos))
	for _, info := range infos {
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
	}
	for index := range entries {
		if entries[index].Type == model.EntryDirectory {
			if err := i.scanDirectory(ctx, backend, storageID, &entries[index], generation); err != nil {
				return err
			}
		}
	}
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
