package processor

import (
	"context"
	"errors"
	"slices"
	"strings"
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

type orderedProcessor struct {
	name  string
	calls *[]string
	match bool
}

func (p orderedProcessor) Name() string           { return p.name }
func (p orderedProcessor) Match(model.Entry) bool { return p.match }
func (p orderedProcessor) Process(_ context.Context, _ model.Entry) error {
	*p.calls = append(*p.calls, p.name)
	return nil
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
	engine := New(cat, queue, NewPlugin("test", func(model.Entry) bool { return true }, matched, unmatched))
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
	continued := New(cat, queue, NewPlugin("failure", func(model.Entry) bool { return true }, failing, after))
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

func TestEngineRunsMatchingPluginsAndStepsInOrder(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "folder.jpg", Path: "folder.jpg", Type: model.EntryFile, Extension: "jpg", ModifiedAt: time.Now().UTC()}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	engine := New(cat, jobs.New(db, time.Minute),
		NewPlugin("image", func(model.Entry) bool { return true },
			orderedProcessor{name: "metadata", calls: &calls, match: true},
			orderedProcessor{name: "skipped", calls: &calls},
			orderedProcessor{name: "catalog", calls: &calls, match: true}),
		NewPlugin("video", func(model.Entry) bool { return false }, orderedProcessor{name: "video", calls: &calls, match: true}),
		NewPlugin("sidecar", func(model.Entry) bool { return true }, orderedProcessor{name: "sidecar", calls: &calls, match: true}),
	)
	if err := engine.EnqueueEntries(ctx, entries); err != nil {
		t.Fatal(err)
	}
	job, err := engine.queue.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(calls, ","), "metadata,catalog,sidecar"; got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
}

func TestSelectPluginsUsesConfiguredOrderAndPresence(t *testing.T) {
	step := orderedProcessor{name: "step", calls: &[]string{}, match: true}
	available := []Plugin{
		NewPlugin("video", func(model.Entry) bool { return true }, step),
		NewPlugin("movies", func(model.Entry) bool { return true }, step),
		NewPlugin("music", func(model.Entry) bool { return true }, step),
	}
	selected, err := SelectPlugins([]string{"music", "video"}, available...)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{selected[0].Name(), selected[1].Name()}; !slices.Equal(got, []string{"music", "video"}) {
		t.Fatalf("selected plugins = %v", got)
	}
	if _, err := SelectPlugins([]string{"video", "video"}, available...); err == nil {
		t.Fatal("duplicate configured plugin was accepted")
	}
	if _, err := SelectPlugins([]string{"unknown"}, available...); err == nil {
		t.Fatal("unknown configured plugin was accepted")
	}
	all, err := SelectPlugins(nil, available...)
	if err != nil || len(all) != len(available) {
		t.Fatalf("default plugins = %v, %v", all, err)
	}
}
