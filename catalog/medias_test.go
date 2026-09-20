package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestMediaCandidatesResolvePerFieldAndRecoverAfterRemoval(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Arrival", Path: "Arrival", Type: model.EntryDirectory}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	id := entries[0].ID
	year := 2016
	if _, err := cat.SetMediaCandidate(ctx, id, "tmdb", MediaCandidate{Kind: "movie", Title: "Arrival", Year: &year, Icon: "cache:poster-key", Backdrop: "cache:background-key", Summary: "A linguist meets visitors."}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetMediaCandidate(ctx, id, "local_artwork", MediaCandidate{Icon: "file:folder-id"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetMediaCandidate(ctx, id, "local_nfo", MediaCandidate{Title: "降临", Summary: "本地简介"}); err != nil {
		t.Fatal(err)
	}
	item, err := cat.MediaForEntry(ctx, id)
	if err != nil || item.Title != "降临" || item.Summary != "本地简介" || item.Icon != "file:folder-id" || item.Backdrop != "cache:background-key" || item.Year == nil || *item.Year != year {
		t.Fatalf("resolved media = %#v, %v", item, err)
	}
	if _, err := cat.ClearMediaCandidate(ctx, id, "local_artwork"); err != nil {
		t.Fatal(err)
	}
	item, err = cat.MediaForEntry(ctx, id)
	if err != nil || item.Icon != "cache:poster-key" || item.Title != "降临" || item.Summary != "本地简介" {
		t.Fatalf("fallback media = %#v, %v", item, err)
	}
	batch, err := cat.MediasForEntries(ctx, []string{id, "absent"})
	if err != nil || len(batch) != 1 || batch[id].Title != "降临" {
		t.Fatalf("batch media = %#v, %v", batch, err)
	}
	if _, err := cat.ClearMediaCandidate(ctx, id, "local_nfo"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ClearMediaCandidate(ctx, id, "tmdb"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaForEntry(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("media row survived last candidate removal: %v", err)
	}
}

func TestMediaCandidateKeepsHighestPriorityTitleEvenIfEqualToFilename(t *testing.T) {
	item := resolveMedia("file", "Arrival", map[string]MediaCandidate{
		"manual": {Title: "Arrival"}, "tmdb": {Title: "降临"},
	})
	if item.Title != "Arrival" || !item.MatchLocked {
		t.Fatalf("manual candidate lost priority: %#v", item)
	}
}

func TestMediaCandidateBatchRollsBackOnMissingEntry(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "movie.mkv", Path: "movie.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	valid := MediaCandidate{Kind: "movie", Title: "Movie"}
	err = cat.SetMediaCandidatesBatch(ctx, []MediaUpdate{
		{FileID: entries[0].ID, Candidates: map[string]*MediaCandidate{"filename": &valid}},
		{FileID: "missing", Candidates: map[string]*MediaCandidate{"filename": &valid}},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("batch error = %v", err)
	}
	if _, err := cat.MediaForEntry(ctx, entries[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first entry was committed despite rollback: %v", err)
	}
}

func TestSemanticMediaFieldsOverrideTechnicalFallback(t *testing.T) {
	year := 2014
	item := resolveMedia("file", "Interstellar.mkv", map[string]MediaCandidate{
		"embedded": {Kind: "video", Title: "Interstellar", Line1: "视频 · 1920 × 1080", Line2: "H264 · AAC", Line3: "2:49:00 · MKV"},
		"filename": {Kind: "movie", Title: "Interstellar", Year: &year, Line1: "电影 · 2014"},
		"tmdb": {Kind: "movie", Title: "星际穿越", Year: &year, Line1: "电影 · 2014", Line2: "Interstellar"},
	})
	if item.Kind != "movie" || item.Title != "星际穿越" || item.Line1 != "电影 · 2014" || item.Line2 != "Interstellar" || item.Line3 != "2:49:00 · MKV" {
		t.Fatalf("resolved semantic media = %#v", item)
	}
}
