package processor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/jobs"
	"github.com/lsongdev/files-go/model"
)

type recordingProcessor struct {
	matched bool
	entries []string
	err     error
}

func (p *recordingProcessor) Name() string           { return "recording" }
func (p *recordingProcessor) Match(model.Entry) bool { return p.matched }
func (p *recordingProcessor) Process(_ context.Context, entry model.Entry) error {
	p.entries = append(p.entries, entry.ID)
	return p.err
}

func TestEngineEnqueuesDeduplicatedFileJobsAndRunsMatchingProcessors(t *testing.T) {
	ctx := context.Background()
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "movie.mp4", Path: "movie.mp4", Type: model.EntryFile, Size: 5, ModifiedAt: time.Now().UTC()},
		{StorageID: "disk", Name: "folder", Path: "folder", Type: model.EntryDirectory},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	queue := jobs.New(db, time.Minute)
	matched := &recordingProcessor{matched: true}
	unmatched := &recordingProcessor{}
	engine := New(cat, queue, matched, unmatched)
	if err := engine.EnqueueEntries(ctx, entries); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnqueueEntries(ctx, entries); err != nil {
		t.Fatal(err)
	}
	job, err := queue.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := queue.Complete(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if len(matched.entries) != 1 || matched.entries[0] != entries[0].ID || len(unmatched.entries) != 0 {
		t.Fatalf("processor calls = matched %v, unmatched %v", matched.entries, unmatched.entries)
	}
	if _, err := queue.Claim(ctx); !errors.Is(err, jobs.ErrNoJob) {
		t.Fatalf("duplicate job remained claimable: %v", err)
	}
	failing := &recordingProcessor{matched: true, err: errors.New("thumbnail failed")}
	after := &recordingProcessor{matched: true}
	continued := New(cat, queue, failing, after)
	if err := continued.ReprocessEntry(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	job, err = queue.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job.Priority != 1000 {
		t.Fatalf("interactive reprocess priority = %d, want 1000", job.Priority)
	}
	err = continued.Handle(ctx, job)
	if err == nil || len(failing.entries) != 1 || len(after.entries) != 1 {
		t.Fatalf("processor isolation = failing %v, after %v, err %v", failing.entries, after.entries, err)
	}
}
