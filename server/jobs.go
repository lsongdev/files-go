package server

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errBusy = errors.New("media jobs are busy")

type jobResult struct {
	done chan struct{}
	err  error
}

// jobs collapses identical work. A canceled waiter does not abort work needed
// by another viewer; the server lifetime and job timeout bound generation.
type jobs struct {
	closed bool
	wg     sync.WaitGroup
	mu     sync.Mutex
	active map[string]*jobResult
	limit  int
}

func (j *jobs) do(ctx, lifetime context.Context, key string, timeout time.Duration, run func(context.Context) error) error {
	j.mu.Lock()
	if j.closed || lifetime.Err() != nil {
		j.mu.Unlock()
		return context.Canceled
	}
	if j.active == nil {
		j.active = map[string]*jobResult{}
	}
	task := j.active[key]
	if task == nil {
		limit := j.limit
		if limit <= 0 {
			limit = 128
		}
		if len(j.active) >= limit {
			j.mu.Unlock()
			return errBusy
		}
		task = &jobResult{done: make(chan struct{})}
		j.active[key] = task
		j.wg.Add(1)
		go func() {
			defer j.wg.Done()
			gen, cancel := context.WithTimeout(lifetime, timeout)
			defer cancel()
			task.err = run(gen)
			j.mu.Lock()
			delete(j.active, key)
			close(task.done)
			j.mu.Unlock()
		}()
	}
	j.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-task.done:
		return task.err
	}
}
func slot(ctx context.Context, pool chan struct{}) (func(), error) {
	select {
	case pool <- struct{}{}:
		return func() { <-pool }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (j *jobs) close() { j.mu.Lock(); j.closed = true; j.mu.Unlock(); j.wg.Wait() }
