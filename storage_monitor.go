package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

// probeStorageAvailability checks only the configured root, never enumerating
// it. A missing mount must not leave a previously scanned storage marked
// online; when it returns, the caller schedules a resumable scan.
func probeStorageAvailability(ctx context.Context, cat *catalog.Catalog, registry *storage.Registry, storageID string, onRecovered func(string)) error {
	backend, ok := registry.Get(storageID)
	if !ok {
		return fmt.Errorf("storage %q is not configured", storageID)
	}
	state, err := cat.Storage(ctx, storageID)
	if err != nil {
		return err
	}
	root, err := backend.Stat(ctx, "")
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrOffline) || (err == nil && root.Type != model.EntryDirectory) {
		if state.State != "offline" {
			return cat.FailScan(ctx, storageID, "offline", "storage root is unavailable")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if state.State == "offline" && onRecovered != nil {
		onRecovered(storageID)
	}
	return nil
}
