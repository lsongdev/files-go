package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestPlaybackStateAndContinueWatching(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Interstellar.mkv", Path: "Interstellar.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	entryID := entries[0].ID
	item, err := cat.SetMediaCandidate(ctx, entryID, "filename", MediaCandidate{Kind: "movie", Title: "Interstellar"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := cat.UpsertPlaybackState(ctx, model.PlaybackState{UserID: "local", EntryID: entryID, PositionMS: 42_000})
	if err != nil || state.PositionMS != 42_000 || state.Played {
		t.Fatalf("state = %#v, %v", state, err)
	}
	items, err := cat.ContinueWatching(ctx, "local", 20)
	if err != nil || len(items) != 1 || items[0].Media == nil || items[0].Media.Title != item.Title {
		t.Fatalf("continue watching = %#v, %v", items, err)
	}
	if _, err := cat.UpsertPlaybackState(ctx, model.PlaybackState{UserID: "local", EntryID: entryID, PositionMS: 42_000, Played: true}); err != nil {
		t.Fatal(err)
	}
	items, err = cat.ContinueWatching(ctx, "local", 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("played continue watching = %#v, %v", items, err)
	}
	if _, err := cat.PlaybackState(ctx, "other", entryID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
}
