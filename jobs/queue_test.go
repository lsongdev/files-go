package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lsongdev/files-go/database"
)

func testQueue(t *testing.T) *Queue {
	t.Helper()
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, time.Minute)
}

func TestQueueDeduplicatesPrioritizesRetriesAndRecoversLeases(t *testing.T) {
	ctx := context.Background()
	queue := testQueue(t)
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return base }

	low, created, err := queue.Enqueue(ctx, "probe", map[string]string{"entryId": "low"}, EnqueueOptions{Key: "low", Priority: 1})
	if err != nil || !created {
		t.Fatalf("enqueue low = %#v, %v, %v", low, created, err)
	}
	high, created, err := queue.Enqueue(ctx, "probe", map[string]string{"entryId": "high"}, EnqueueOptions{Key: "high", Priority: 10})
	if err != nil || !created {
		t.Fatalf("enqueue high = %#v, %v, %v", high, created, err)
	}
	duplicate, created, err := queue.Enqueue(ctx, "probe", nil, EnqueueOptions{Key: "high"})
	if err != nil || created || duplicate.ID != high.ID {
		t.Fatalf("duplicate = %#v, %v, %v", duplicate, created, err)
	}

	claimed, err := queue.Claim(ctx)
	if err != nil || claimed.ID != high.ID || claimed.Attempts != 1 || claimed.State != StateRunning {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	if err := queue.Fail(ctx, claimed, errors.New("temporary"), 0); err != nil {
		t.Fatal(err)
	}
	claimed, err = queue.Claim(ctx)
	if err != nil || claimed.ID != high.ID || claimed.Attempts != 2 {
		t.Fatalf("retry claim = %#v, %v", claimed, err)
	}
	if err := queue.Complete(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}

	claimed, err = queue.Claim(ctx)
	if err != nil || claimed.ID != low.ID {
		t.Fatalf("low claim = %#v, %v", claimed, err)
	}
	base = base.Add(2 * time.Minute)
	recovered, err := queue.Claim(ctx)
	if err != nil || recovered.ID != low.ID || recovered.Attempts != 2 || recovered.Error != "" {
		t.Fatalf("recovered claim = %#v, %v", recovered, err)
	}
}

func TestQueueFailsPermanentlyAtAttemptLimit(t *testing.T) {
	ctx := context.Background()
	queue := testQueue(t)
	job, _, err := queue.Enqueue(ctx, "broken", nil, EnqueueOptions{Key: "one", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	job, err = queue.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Fail(ctx, job, errors.New("permanent failure"), 0); err != nil {
		t.Fatal(err)
	}
	failed, err := queue.byTypeKey(ctx, "broken", "one")
	if err != nil || failed.State != StateFailed || failed.Error != "permanent failure" || failed.FinishedAt == nil {
		t.Fatalf("failed job = %#v, %v", failed, err)
	}
	if _, err := queue.Claim(ctx); !errors.Is(err, ErrNoJob) {
		t.Fatalf("claim after permanent failure = %v", err)
	}
}

func TestWorkerPoolRunsHandlerAndStopsGracefully(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := testQueue(t)
	pool := NewPool(queue, 2)
	handled := make(chan string, 1)
	pool.Handle("work", func(_ context.Context, job *Job) error {
		handled <- job.Key
		return nil
	})
	job, _, err := queue.Enqueue(ctx, "work", map[string]bool{"ok": true}, EnqueueOptions{Key: "job-1"})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(ctx)
	select {
	case key := <-handled:
		if key != "job-1" {
			t.Fatalf("handled key = %q", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not handle queued job")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, findErr := queue.byTypeKey(context.Background(), "work", "job-1")
		if findErr == nil && current.State == StateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not complete: %#v, %v", job.ID, current, findErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	pool.Wait()
}

func TestJobClaimUsesPendingIndex(t *testing.T) {
	queue := testQueue(t)
	rows, err := queue.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM jobs
		WHERE state='pending' AND (run_after IS NULL OR run_after<=?)
		ORDER BY priority DESC, COALESCE(run_after, created_at), created_at, id LIMIT 1`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if !strings.Contains(strings.Join(details, "\n"), "idx_jobs_claim") {
		t.Fatalf("query plan does not use idx_jobs_claim: %v", details)
	}
}
