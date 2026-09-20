package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

func TestProbeStorageAvailabilityMarksMissingRootOfflineAndSchedulesRecovery(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "disk")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", backend); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "movie.mkv", Path: "movie.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.CompleteScan(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	away := root + "-offline"
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	if err := probeStorageAvailability(ctx, cat, registry, "disk", nil); err != nil {
		t.Fatal(err)
	}
	state, err := cat.Storage(ctx, "disk")
	if err != nil || state.State != "offline" {
		t.Fatalf("storage state = %#v, %v", state, err)
	}
	entry, err := cat.Entry(ctx, entries[0].ID)
	if err != nil || !entry.Available {
		t.Fatalf("storage outage changed entry presence = %#v, %v", entry, err)
	}
	if err := os.Rename(away, root); err != nil {
		t.Fatal(err)
	}
	recovered := false
	if err := probeStorageAvailability(ctx, cat, registry, "disk", func(id string) { recovered = id == "disk" }); err != nil || !recovered {
		t.Fatalf("recovery scan scheduled = %v, %v", recovered, err)
	}
}
