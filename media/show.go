package media

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

type showFlight struct {
	done   chan struct{}
	item   *Item
	review string
	err    error
}

// resolveShow shares identification across episodes, including concurrent
// workers. Only successful resolutions (including an unmatched result) are
// cached; the key includes local evidence, NFO identity and manual choice.
func (l *Library) resolveShow(ctx context.Context, m *Meta, title string, year int, ev evidence) (*Item, string, error) {
	if l.TMDB == nil {
		return nil, "", nil
	}
	input, _ := json.Marshal([]any{title, year, ev, m.Match, m.Locked, l.TMDB.client.Language()})
	key := fmt.Sprintf("show:v1:%x", sha256.Sum256(input))
	l.mu.Lock()
	if l.shows == nil {
		l.shows = make(map[string]*showFlight)
	}
	if job := l.shows[key]; job != nil {
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-job.done:
			return job.item, job.review, job.err
		}
	}
	job := &showFlight{done: make(chan struct{})}
	l.shows[key] = job
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.shows, key); close(job.done); l.mu.Unlock() }()
	var cached struct {
		Item   *Item
		Review string
	}
	if data, ok := l.Store.CacheGet(ctx, key, 30*24*time.Hour); ok && json.Unmarshal(data, &cached) == nil {
		job.item, job.review = cached.Item, cached.Review
		return job.item, job.review, nil
	}
	job.item, job.err = l.find(ctx, m, "tv", title, year, ev)
	job.review = m.Review
	if job.item == nil && job.review == "" {
		job.review = "unmatched"
	}
	if job.err == nil {
		cached.Item, cached.Review = job.item, job.review
		data, _ := json.Marshal(cached)
		job.err = l.Store.CachePut(ctx, key, data)
	}
	return job.item, job.review, job.err
}
