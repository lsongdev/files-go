package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNoJob    = errors.New("no runnable job")
	ErrNotFound = errors.New("job not found")
)

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

type Job struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Key         string          `json:"key,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	State       State           `json:"state"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"maxAttempts"`
	Priority    int             `json:"priority"`
	RunAfter    *time.Time      `json:"runAfter,omitempty"`
	LeaseUntil  *time.Time      `json:"leaseUntil,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type EnqueueOptions struct {
	Key         string
	Priority    int
	RunAfter    time.Time
	MaxAttempts int
}

type Queue struct {
	db       *sql.DB
	lease    time.Duration
	notify   chan struct{}
	now      func() time.Time
	recoverM sync.Mutex
}

func New(db *sql.DB, lease time.Duration) *Queue {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	return &Queue{db: db, lease: lease, notify: make(chan struct{}, 1), now: func() time.Time { return time.Now().UTC() }}
}

func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any, opts EnqueueOptions) (*Job, bool, error) {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return nil, false, errors.New("job type is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("marshal job payload: %w", err)
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	now := q.now()
	var key any
	if opts.Key != "" {
		key = opts.Key
	}
	var runAfter any
	if !opts.RunAfter.IsZero() {
		runAfter = opts.RunAfter.UTC()
	}
	row := q.db.QueryRowContext(ctx, `INSERT INTO jobs
		(id, type, key, payload, state, attempts, max_attempts, priority, run_after, created_at)
		VALUES (?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?)
		ON CONFLICT(type, key) DO NOTHING
		RETURNING id, type, COALESCE(key, ''), payload, state, attempts, max_attempts, priority,
			run_after, lease_until, created_at, started_at, finished_at, COALESCE(error, '')`,
		uuid.Must(uuid.NewV7()).String(), jobType, key, string(data), maxAttempts, opts.Priority, runAfter, now)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		existing, findErr := q.byTypeKey(ctx, jobType, opts.Key)
		return existing, false, findErr
	}
	if err != nil {
		return nil, false, err
	}
	q.wake()
	return job, true, nil
}

func (q *Queue) Claim(ctx context.Context) (*Job, error) {
	if err := q.recoverExpired(ctx); err != nil {
		return nil, err
	}
	now := q.now()
	leaseUntil := now.Add(q.lease)
	job, err := scanJob(q.db.QueryRowContext(ctx, `UPDATE jobs SET
		state='running', attempts=attempts+1, started_at=?, lease_until=?, finished_at=NULL, error=NULL
		WHERE id = (
			SELECT id FROM jobs
			WHERE state='pending' AND (run_after IS NULL OR run_after<=?)
			ORDER BY priority DESC, COALESCE(run_after, created_at), created_at, id
			LIMIT 1
		)
		RETURNING id, type, COALESCE(key, ''), payload, state, attempts, max_attempts, priority,
			run_after, lease_until, created_at, started_at, finished_at, COALESCE(error, '')`, now, leaseUntil, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoJob
	}
	return job, err
}

func (q *Queue) Complete(ctx context.Context, id string) error {
	result, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='done', finished_at=?, lease_until=NULL, error=NULL WHERE id=? AND state='running'`, q.now(), id)
	return affected(result, err)
}

func (q *Queue) Fail(ctx context.Context, job *Job, cause error, delay time.Duration) error {
	if job == nil {
		return ErrNotFound
	}
	message := errorMessage(cause)
	now := q.now()
	if job.Attempts >= job.MaxAttempts {
		return q.failPermanently(ctx, job.ID, message)
	}
	result, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', run_after=?, started_at=NULL, lease_until=NULL, error=? WHERE id=? AND state='running'`, now.Add(delay), message, job.ID)
	if err == nil {
		q.wake()
	}
	return affected(result, err)
}

func (q *Queue) FailPermanently(ctx context.Context, id string, cause error) error {
	return q.failPermanently(ctx, id, errorMessage(cause))
}

func (q *Queue) failPermanently(ctx context.Context, id, message string) error {
	result, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', finished_at=?, lease_until=NULL, error=? WHERE id=? AND state='running'`, q.now(), message, id)
	return affected(result, err)
}

func (q *Queue) recoverExpired(ctx context.Context) error {
	q.recoverM.Lock()
	defer q.recoverM.Unlock()
	now := q.now()
	if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', finished_at=?, lease_until=NULL, error='worker lease expired' WHERE state='running' AND lease_until<=? AND attempts>=max_attempts`, now, now); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', run_after=?, started_at=NULL, lease_until=NULL, error='worker lease expired' WHERE state='running' AND lease_until<=? AND attempts<max_attempts`, now, now)
	return err
}

func (q *Queue) byTypeKey(ctx context.Context, jobType, key string) (*Job, error) {
	job, err := scanJob(q.db.QueryRowContext(ctx, `SELECT id, type, COALESCE(key, ''), payload, state, attempts, max_attempts, priority,
		run_after, lease_until, created_at, started_at, finished_at, COALESCE(error, '') FROM jobs WHERE type=? AND key=?`, jobType, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

type rowScanner interface{ Scan(...any) error }

func scanJob(row rowScanner) (*Job, error) {
	var job Job
	var payload string
	var runAfter, leaseUntil, startedAt, finishedAt sql.NullTime
	if err := row.Scan(&job.ID, &job.Type, &job.Key, &payload, &job.State, &job.Attempts, &job.MaxAttempts,
		&job.Priority, &runAfter, &leaseUntil, &job.CreatedAt, &startedAt, &finishedAt, &job.Error); err != nil {
		return nil, err
	}
	job.Payload = json.RawMessage(payload)
	if runAfter.Valid {
		job.RunAfter = &runAfter.Time
	}
	if leaseUntil.Valid {
		job.LeaseUntil = &leaseUntil.Time
	}
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		job.FinishedAt = &finishedAt.Time
	}
	return &job, nil
}

func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func errorMessage(err error) string {
	if err == nil {
		return "job failed"
	}
	message := err.Error()
	if len(message) > 4096 {
		message = message[:4096]
	}
	return message
}

func (q *Queue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
