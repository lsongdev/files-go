package indexer

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"path"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

// ScanSubtree reconciles one configured library root or a directory below it.
// It does not advance the storage-wide scan generation or invalidate entries
// belonging to other libraries, so it is suitable for small development tests.
func (i *Indexer) ScanSubtree(ctx context.Context, storageID, rootPath string) error {
	rootPath = strings.Trim(path.Clean(filepath.ToSlash(rootPath)), "/")
	if rootPath == "." {
		rootPath = ""
	}
	if rootPath == ".." || strings.HasPrefix(rootPath, "../") || !i.withinConfiguredSource(storageID, rootPath) {
		return storage.ErrPathTraversal
	}
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
		return storage.ErrOffline
	}
	state, err := i.catalog.Storage(ctx, storageID)
	if err != nil {
		return err
	}
	parent, err := i.catalog.EnsureRoot(ctx, storageID, state.ScanGeneration)
	if err != nil {
		return err
	}
	if rootPath != "" {
		prefix := ""
		for _, component := range strings.Split(rootPath, "/") {
			prefix = path.Join(prefix, component)
			info, err := backend.Stat(ctx, prefix)
			if errors.Is(err, storage.ErrNotFound) {
				if indexed, lookupErr := i.catalog.EntryByPath(ctx, storageID, prefix); lookupErr == nil {
					if err := i.catalog.MarkEntryTreeUnavailable(ctx, *indexed); err != nil {
						return err
					}
					return i.reconcileRemovedEntry(ctx, *indexed)
				} else if !errors.Is(lookupErr, catalog.ErrNotFound) {
					return lookupErr
				}
				return nil
			}
			if err != nil {
				return err
			}
			if info.Type != model.EntryDirectory {
				return fmt.Errorf("scan root %q is not a directory", prefix)
			}
			parentID := parent.ID
			updated, err := i.catalog.UpsertEntries(ctx, []model.Entry{{StorageID: storageID, ParentID: &parentID,
				Name: info.Name, Path: info.Path, Type: info.Type, Size: info.Size,
				ModifiedAt: info.ModifiedAt, Inode: info.Inode, Device: info.Device}}, state.ScanGeneration)
			if err != nil {
				return err
			}
			parent = &updated[0]
		}
	}
	return i.scanScopedDirectory(ctx, backend, storageID, *parent, state.ScanGeneration)
}

func (i *Indexer) withinConfiguredSource(storageID, value string) bool {
	i.mu.Lock()
	roots, scoped := i.scope[storageID]
	i.mu.Unlock()
	if !scoped {
		return false
	}
	for _, root := range roots {
		if root == "" || value == root || strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}

func (i *Indexer) CanScanSubtree(storageID, value string) bool {
	return i.withinConfiguredSource(storageID, value)
}

func (i *Indexer) scanScopedDirectory(ctx context.Context, backend storage.Storage, storageID string, parent model.Entry, generation int64) error {
	infos, err := backend.ReadDir(ctx, parent.Path)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		seen[info.Name] = true
	}
	old, err := i.catalog.AvailableChildren(ctx, parent.ID)
	if err != nil {
		return err
	}
	for _, entry := range old {
		if !seen[entry.Name] {
			if err := i.catalog.MarkEntryTreeUnavailable(ctx, entry); err != nil {
				return err
			}
			if err := i.reconcileRemovedEntry(ctx, entry); err != nil {
				return err
			}
		}
	}
	const batchSize = 500
	for start := 0; start < len(infos); start += batchSize {
		end := start + batchSize
		if end > len(infos) {
			end = len(infos)
		}
		entries := make([]model.Entry, 0, end-start)
		for _, info := range infos[start:end] {
			ext := strings.TrimPrefix(strings.ToLower(path.Ext(info.Name)), ".")
			mimeType := ""
			if ext != "" {
				mimeType = mime.TypeByExtension("." + ext)
			}
			parentID := parent.ID
			entries = append(entries, model.Entry{StorageID: storageID, ParentID: &parentID,
				Name: info.Name, Path: info.Path, Type: info.Type, Size: info.Size,
				ModifiedAt: info.ModifiedAt, Inode: info.Inode, Device: info.Device,
				MIME: mimeType, Extension: ext})
		}
		updated, err := i.catalog.UpsertEntries(ctx, entries, generation)
		if err != nil {
			return err
		}
		for _, entry := range updated {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type == model.EntryFile {
				// A scoped scan is an explicit rebuild request, so unchanged files
				// must run their enhancement plugins again as well.
				if err := i.ReprocessEntry(ctx, entry); err != nil {
					return err
				}
			}
			if entry.Type == model.EntryDirectory {
				if err := i.scanScopedDirectory(ctx, backend, storageID, entry, generation); err != nil {
					if errors.Is(err, storage.ErrNotFound) {
						if markErr := i.catalog.MarkEntryTreeUnavailable(ctx, entry); markErr != nil {
							return markErr
						}
						if reconcileErr := i.reconcileRemovedEntry(ctx, entry); reconcileErr != nil {
							return reconcileErr
						}
						continue // The directory disappeared after its parent was read.
					}
					return err
				}
			}
		}
	}
	return nil
}
