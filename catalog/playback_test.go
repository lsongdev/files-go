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
	item, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: "Interstellar"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := cat.UpsertPlaybackState(ctx, model.PlaybackState{UserID: "local", MediaID: item.ID, PositionMS: 42_000})
	if err != nil || state.PositionMS != 42_000 || state.Played {
		t.Fatalf("state = %#v, %v", state, err)
	}
	items, err := cat.ContinueWatching(ctx, "local", 20)
	if err != nil || len(items) != 1 || items[0].Media == nil || items[0].Media.Title != item.Title {
		t.Fatalf("continue watching = %#v, %v", items, err)
	}
	if _, err := cat.UpsertPlaybackState(ctx, model.PlaybackState{UserID: "local", MediaID: item.ID, PositionMS: 42_000, Played: true}); err != nil {
		t.Fatal(err)
	}
	items, err = cat.ContinueWatching(ctx, "local", 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("played continue watching = %#v, %v", items, err)
	}
	if _, err := cat.PlaybackState(ctx, "other", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
}
