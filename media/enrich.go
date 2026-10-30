// Package media enriches indexed files with metadata derived from their
// type, name and embedded information.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lsongdev/files-go/store"
)

// Meta is the enrichment result stored as JSON on the file row. Detailed,
// type-specific values go into Info as display-ready key/value pairs.
type Meta struct {
	Kind        string            `json:"kind,omitempty"` // movie, episode, show, season, video, audio, photo, book, app ...
	Title       string            `json:"title,omitempty"`
	Original    string            `json:"original,omitempty"`
	Year        int               `json:"year,omitempty"`
	Season      int               `json:"season,omitempty"`
	Episode     int               `json:"episode,omitempty"`
	Show        string            `json:"show,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Artist      string            `json:"artist,omitempty"`
	AlbumArtist string            `json:"albumArtist,omitempty"`
	Tracks      int               `json:"tracks,omitempty"`
	Albums      int               `json:"albums,omitempty"`
	Files       int               `json:"files,omitempty"`
	Series      *Series           `json:"series,omitempty"`
	Album       string            `json:"album,omitempty"`
	Author      string            `json:"author,omitempty"`
	Duration    float64           `json:"duration,omitempty"` // seconds
	Width       int               `json:"width,omitempty"`
	Height      int               `json:"height,omitempty"`
	Container   string            `json:"container,omitempty"`
	Video       string            `json:"video,omitempty"` // codec
	Audio       string            `json:"audio,omitempty"` // codec
	Depth       int               `json:"depth,omitempty"` // video bit depth
	Subtitles   []Subtitle        `json:"subtitles,omitempty"`
	Rating      float64           `json:"rating,omitempty"`
	Genres      []string          `json:"genres,omitempty"`
	Match       *store.Match      `json:"match,omitempty"`
	Locked      bool              `json:"locked,omitempty"`
	Review      string            `json:"review,omitempty"` // ambiguous or unmatched: worth a manual look
	Info        map[string]string `json:"info,omitempty"`
	// Poster and Backdrop reference artwork: "self" (frame or embedded cover
	// of this file), "zip:<entry>", "file:<id>" or "tmdb:<path>".
	ArtworkVersion string `json:"artworkVersion,omitempty"`
	Poster         string `json:"poster,omitempty"`
	Backdrop       string `json:"backdrop,omitempty"`
}

// Subtitle is an embedded subtitle stream.
type Subtitle struct {
	Index   int    `json:"index"` // among subtitle streams
	Codec   string `json:"codec"`
	Lang    string `json:"lang,omitempty"`
	Title   string `json:"title,omitempty"`
	Default bool   `json:"default,omitempty"`
	Forced  bool   `json:"forced,omitempty"`
}

// Text reports whether the stream can be converted to WebVTT; bitmap
// subtitles (PGS, VobSub) cannot.
func (s Subtitle) Text() bool {
	switch s.Codec {
	case "subrip", "srt", "ass", "ssa", "mov_text", "webvtt", "text":
		return true
	}
	return false
}

func (m *Meta) Set(key, value string) {
	if value = strings.TrimSpace(value); value == "" {
		return
	}
	if m.Info == nil {
		m.Info = map[string]string{}
	}
	m.Info[key] = value
}

func (m *Meta) empty() bool {
	b, _ := json.Marshal(m)
	return string(b) == "{}"
}

// Enricher adds one kind of metadata. Enrichers run in order on the same
// Meta, so later ones can build on earlier results (e.g. movie lookup after
// ffprobe). Enrich may adjust f.Kind when the content disagrees with the name.
type Enricher interface {
	Match(f *store.File) bool
	Enrich(ctx context.Context, f *store.File, m *Meta) error
}

// ErrTransient marks failures worth retrying later, e.g. network errors.
var ErrTransient = errors.New("temporary failure")

// Pipeline runs enrichers in the background over rows whose meta_ver is
// older than Version; the files table itself is the work queue.
type Pipeline struct {
	Version   int
	store     *store.Store
	enrichers []Enricher
	hot       chan int64
	wake      chan struct{}

	mu       sync.Mutex
	inflight map[int64]bool // sent to a worker and not yet saved
}

// NewPipeline gets its Version from the store for the code version plus a
// configuration tag (e.g. whether TMDB is enabled), so changing either
// re-enriches everything in the background.
func NewPipeline(ctx context.Context, st *store.Store, tag string, enrichers ...Enricher) (*Pipeline, error) {
	version, err := st.Version(ctx, "v9:"+tag)
	if err != nil {
		return nil, err
	}
	return &Pipeline{Version: version, store: st, enrichers: enrichers,
		hot: make(chan int64, 512), wake: make(chan struct{}, 1), inflight: map[int64]bool{}}, nil
}

// Prioritize moves files a user is looking at to the front of the queue.
func (p *Pipeline) Prioritize(files []store.File) {
	for _, f := range files {
		if f.MetaVer != p.Version {
			select {
			case p.hot <- f.ID:
			default:
			}
		}
	}
	p.Wake()
}

func (p *Pipeline) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run feeds pending rows to a pool of workers and saves results in small
// batches. A slow file (a large probe, a TMDB round trip) only occupies
// its own worker.
func (p *Pipeline) Run(ctx context.Context, workers int) {
	jobs := make(chan store.File)
	results := make(chan store.Result, 64)
	var wg sync.WaitGroup
	for range max(1, workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				result, err := p.Enrich(ctx, f)
				if err != nil { // transient: leave it pending and back off
					if ctx.Err() == nil {
						if saveErr := p.store.Retry(ctx, f, err); saveErr != nil {
							log.Printf("save retry: %v", saveErr)
						}
					}
					p.mu.Lock()
					delete(p.inflight, f.ID)
					p.mu.Unlock()
					if ctx.Err() == nil {
						log.Printf("enrichment retry for %s: %v", f.Path, err)
					}
					continue
				}
				results <- result
			}
		}()
	}
	saved := make(chan struct{})
	go func() {
		defer close(saved)
		p.save(ctx, results)
	}()
	p.feed(ctx, jobs)
	close(jobs)
	wg.Wait()
	close(results)
	<-saved
}

func (p *Pipeline) feed(ctx context.Context, jobs chan<- store.File) {
	send := func(f store.File) bool {
		p.mu.Lock()
		if f.RetryAfter > time.Now().UnixMilli() {
			p.mu.Unlock()
			return true
		}
		busy := p.inflight[f.ID]
		p.inflight[f.ID] = true
		p.mu.Unlock()
		if busy {
			return true
		}
		select {
		case jobs <- f:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var cursor store.Cursor
	for ctx.Err() == nil {
		select {
		case id := <-p.hot:
			if f, err := p.store.Get(ctx, id); err == nil && f.MetaVer != p.Version {
				send(f)
			}
			continue
		default:
		}
		files, err := p.store.Pending(ctx, p.Version, cursor, 32)
		if err != nil && ctx.Err() == nil {
			log.Printf("load pending files: %v", err)
		}
		if len(files) == 0 {
			if cursor == (store.Cursor{}) {
				select { // nothing pending at all
				case <-ctx.Done():
				case <-p.wake:
				case <-time.After(time.Second):
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			cursor = store.Cursor{} // start over to pick up rows changed behind the cursor
			continue
		}
		for _, f := range files {
			cursor = store.Cursor{MetaVer: f.MetaVer, ID: f.ID}
			if !send(f) {
				return
			}
		}
	}
}

func (p *Pipeline) save(ctx context.Context, results <-chan store.Result) {
	var batch []store.Result
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := p.store.SaveResults(context.WithoutCancel(ctx), batch); err != nil && !errors.Is(err, store.ErrStale) {
			log.Printf("save enrichment: %v", err)
		}
		p.mu.Lock()
		for _, r := range batch {
			delete(p.inflight, r.ID)
		}
		p.mu.Unlock()
		batch = batch[:0]
		p.Wake()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case r, ok := <-results:
			if !ok {
				flush()
				return
			}
			if batch = append(batch, r); len(batch) >= 200 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Enrich runs all matching enrichers for one file. Empty files, e.g. cover
// placeholders a scraper never filled, have nothing to read.
func (p *Pipeline) Enrich(ctx context.Context, f store.File) (store.Result, error) {
	if !f.Dir && f.Size == 0 {
		return store.Result{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Version: p.Version}, nil
	}
	var choice *store.Match
	if f.Dir || f.Kind == "video" {
		var err error
		choice, err = p.store.Match(ctx, f)
		if err != nil {
			return store.Result{}, err
		}
		if f.Dir && choice != nil {
			owner, err := p.store.MatchOwner(ctx, f)
			if err != nil {
				return store.Result{}, err
			}
			if owner.ID != f.ID {
				choice = nil
			} // inherited directory locks are derived from their members
		}
	}
	m := &Meta{Match: choice, Locked: choice != nil}
	var failures []string
	for _, e := range p.enrichers {
		if !e.Match(&f) {
			continue
		}
		if err := e.Enrich(ctx, &f, m); err != nil {
			if errors.Is(err, ErrTransient) || ctx.Err() != nil {
				return store.Result{}, err
			}
			failures = append(failures, err.Error())
		}
	}
	result := store.Result{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Version: p.Version, Err: strings.Join(failures, "; ")}
	if !m.empty() {
		result.Meta, _ = json.Marshal(m)
		if m.Title != "" && m.Title != f.Name {
			result.Title = m.Title
		}
	}
	return result, nil
}

// Rematch re-runs enrichment for one file immediately.
func (p *Pipeline) Rematch(ctx context.Context, f store.File) error {
	result, err := p.Enrich(ctx, f)
	if err != nil {
		return err
	}
	err = p.store.SaveResults(ctx, []store.Result{result})
	p.Wake()
	return err
}
