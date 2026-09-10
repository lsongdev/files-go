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
			_ = p.queue.FailPermanently(context.WithoutCancel(ctx), job.ID, fmt.Errorf("no handler registered for job type %q", job.Type))
			continue
		}
		if err := callHandler(ctx, handler, job); err != nil {
			delay := time.Second << min(job.Attempts-1, 6)
			_ = p.queue.Fail(context.WithoutCancel(ctx), job, err, delay)
			continue
		}
		_ = p.queue.Complete(context.WithoutCancel(ctx), job.ID)
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
