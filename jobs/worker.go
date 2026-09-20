package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Handler func(context.Context, *Job) error

type Pool struct {
	queue       *Queue
	concurrency int
	handlers    map[string]Handler
	poll        time.Duration
	wg          sync.WaitGroup
	start       sync.Once
}

func NewPool(queue *Queue, concurrency int) *Pool {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Pool{queue: queue, concurrency: concurrency, handlers: make(map[string]Handler), poll: time.Second}
}

func (p *Pool) Handle(jobType string, handler Handler) {
	if jobType == "" || handler == nil {
		panic("job type and handler are required")
	}
	p.handlers[jobType] = handler
}

func (p *Pool) Start(ctx context.Context) {
	p.start.Do(func() {
		for range p.concurrency {
			p.wg.Add(1)
			go p.worker(ctx)
		}
	})
}

func (p *Pool) Wait() { p.wg.Wait() }

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		job, err := p.queue.Claim(ctx)
		if errors.Is(err, ErrNoJob) {
			select {
			case <-ctx.Done():
				return
			case <-p.queue.notify:
			case <-time.After(p.poll):
			}
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.poll):
			}
			continue
		}
		handler, ok := p.handlers[job.Type]
		if !ok {
			_ = p.queue.failPermanentlyAttempt(context.WithoutCancel(ctx), job.ID, job.Attempts, fmt.Sprintf("no handler registered for job type %q", job.Type))
			continue
		}
		if err := p.callHandlerWithLease(ctx, handler, job); err != nil {
			delay := time.Second << min(job.Attempts-1, 6)
			_ = p.queue.Fail(context.WithoutCancel(ctx), job, err, delay)
			continue
		}
		_ = p.queue.CompleteJob(context.WithoutCancel(ctx), job)
	}
}

func callHandler(ctx context.Context, handler Handler, job *Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panic: %v", recovered)
		}
	}()
	return handler(ctx, job)
}

func (p *Pool) callHandlerWithLease(ctx context.Context, handler Handler, job *Job) error {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	interval := p.queue.LeaseDuration() / 3
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if err := p.queue.Renew(context.WithoutCancel(jobCtx), job); err != nil {
					select {
					case heartbeatErr <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	err := callHandler(jobCtx, handler, job)
	close(done)
	select {
	case renewErr := <-heartbeatErr:
		return fmt.Errorf("renew job lease: %w", renewErr)
	default:
		return err
	}
}
