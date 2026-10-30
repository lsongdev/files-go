package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// breaker is per provider. It suppresses repeated failing remote calls without
// pausing local enrichers or persisting temporary service state.
type breaker struct {
	mu       sync.Mutex
	failures int
	until    time.Time
	probing  bool
}

func (b *breaker) before(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.until.IsZero() {
		if time.Now().Before(b.until) || b.probing {
			return fmt.Errorf("%w: catalog cooling down", ErrTransient)
		}
		b.probing = true
	}
	return nil
}
func (b *breaker) after(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	if errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, ErrTransient) {
		b.failures++
		if b.failures >= 3 {
			b.until = time.Now().Add(30 * time.Second)
		}
		return
	}
	b.failures = 0
	b.until = time.Time{}
}
