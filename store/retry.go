package store

import (
	"context"
	"time"
)

// Retry preserves a failed task and its backoff across restarts. A changed
// revision must not inherit an older worker's failure or retry schedule.
func (s *Store) Retry(ctx context.Context, f File, cause error) error {
	attempts := f.RetryCount + 1
	delay := min(5*time.Minute, 5*time.Second*time.Duration(1<<min(attempts-1, 6)))
	_, err := s.db.ExecContext(ctx, `UPDATE files SET retry_count=?,retry_after=?,meta_err=? WHERE id=? AND revision=?`, attempts, time.Now().Add(delay).UnixMilli(), cause.Error(), f.ID, f.Revision)
	return err
}
