// Package hls serves complete VOD timelines backed by lazily generated fMP4
// fragments. It knows nothing about a media library, HTTP routing, authentication,
// or codec selection. Callers supply trusted ffmpeg mapping/codec options.
package hls

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("HLS playback or fragment not found")
	ErrClosed        = errors.New("HLS manager closed")
	ErrSourceChanged = errors.New("media changed; open a new playback")
)

type Config struct {
	// CacheDir is a parent directory. A private temporary subdirectory is
	// created for this manager and removed on Close or context cancellation.
	CacheDir        string
	FFmpeg          string
	FFprobe         string
	SegmentDuration time.Duration // default 6 s; copied video follows keyframes
	IdleTimeout     time.Duration // default 30 min, for tokens and cached fragments
	JobTimeout      time.Duration // default 2 min, including waiting for a slot
	MaxBytes        int64         // default 512 MiB of completed fragments
	MaxConcurrent   int           // default 2 probe/ffmpeg processes
	MaxSessions     int           // default 128 independent playback tokens
}

// Source describes a local file and its output rendition. InputArgs precede
// -i; OutputArgs supply stream mapping and codecs, never muxer/output options.
// CopyVideo requires OutputArgs to copy v:0; it enables keyframe-aligned cuts.
// Hardware and codec policy belong to the caller, outside this package.
type Source struct {
	Path       string
	CopyVideo  bool
	InputArgs  []string
	OutputArgs []string
}

type Playback struct {
	Key      string
	Duration float64
}

type stream struct {
	source   Source
	segments []segment
	size     int64
	mtime    time.Time
	last     time.Time
}

type playback struct {
	stream *stream
	last   time.Time
}

type cached struct {
	dir  string
	size int64
	last time.Time
}

type job struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	err     error
}

type Manager struct {
	cfg    Config
	dir    string
	ctx    context.Context
	cancel context.CancelFunc
	closed chan struct{}
	slots  chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	stop   bool
	bytes  int64
	media  map[string]*stream
	plays  map[string]*playback
	cache  map[string]*cached
	jobs   map[string]*job
}

func New(ctx context.Context, cfg Config) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.FFmpeg == "" {
		cfg.FFmpeg = "ffmpeg"
	}
	if cfg.FFprobe == "" {
		cfg.FFprobe = "ffprobe"
	}
	if cfg.SegmentDuration <= 0 {
		cfg.SegmentDuration = 6 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 2 * time.Minute
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 512 << 20
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 128
	}
	if cfg.CacheDir == "" {
		return nil, fmt.Errorf("HLS cache directory is required")
	}
	var err error
	cfg.CacheDir, err = filepath.Abs(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(cfg.CacheDir, "hls-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{cfg: cfg, dir: dir, ctx: ctx, cancel: cancel, closed: make(chan struct{}),
		slots: make(chan struct{}, cfg.MaxConcurrent), media: make(map[string]*stream),
		plays: make(map[string]*playback), cache: make(map[string]*cached), jobs: make(map[string]*job)}
	go m.maintain()
	return m, nil
}

// Open reads metadata and, for copied video, keyframe packet headers. It never
// generates media. Multiple viewers share the same rendition cache but have
// separate capability tokens; opening or closing one cannot stop another.
func (m *Manager) Open(ctx context.Context, src Source) (Playback, error) {
	if err := ctx.Err(); err != nil {
		return Playback{}, err
	}
	path, err := filepath.Abs(src.Path)
	if err != nil {
		return Playback{}, err
	}
	src.Path = path
	src.InputArgs = append([]string(nil), src.InputArgs...)
	src.OutputArgs = append([]string(nil), src.OutputArgs...)
	info, err := os.Stat(src.Path)
	if err != nil {
		return Playback{}, err
	}
	if !info.Mode().IsRegular() {
		return Playback{}, fmt.Errorf("media source is not a regular file")
	}
	encoded, _ := json.Marshal(struct {
		Source         Source
		Size, Modified int64
	}{src, info.Size(), info.ModTime().UnixNano()})
	sum := sha256.Sum256(encoded)
	id := hex.EncodeToString(sum[:])
	m.mu.Lock()
	prepared := m.media[id]
	if prepared != nil {
		prepared.last = time.Now()
	}
	m.mu.Unlock()
	// A second viewer of an already-probed rendition needs no process slot.
	if prepared == nil {
		err = m.do(ctx, "probe:"+id, func(ctx context.Context) error {
			m.mu.Lock()
			ready := m.media[id] != nil
			m.mu.Unlock()
			if ready {
				return nil
			}
			segments, err := m.timeline(ctx, src)
			if err != nil {
				return err
			}
			if err := unchanged(src.Path, info.Size(), info.ModTime()); err != nil {
				return err
			}
			m.mu.Lock()
			m.media[id] = &stream{source: src, segments: segments, size: info.Size(), mtime: info.ModTime(), last: time.Now()}
			m.mu.Unlock()
			return nil
		})
	}
	if err != nil {
		return Playback{}, err
	}
	key := make([]byte, 24)
	if _, err := rand.Read(key); err != nil {
		return Playback{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop {
		return Playback{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return Playback{}, err
	}
	s := m.media[id]
	if s == nil {
		return Playback{}, ErrNotFound
	}
	s.last = time.Now()
	for len(m.plays) >= m.cfg.MaxSessions {
		m.expireOldestPlayback()
	}
	token := hex.EncodeToString(key)
	m.plays[token] = &playback{stream: s, last: time.Now()}
	last := s.segments[len(s.segments)-1]
	return Playback{Key: token, Duration: last.start + last.duration}, nil
}

// Playlist returns a stable, complete VOD playlist with relative fragment URLs.
func (m *Manager) Playlist(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.lookup(key)
	if err != nil {
		return nil, err
	}
	return playlist(s.segments), nil
}

// Segment opens a cached fragment or waits for its on-demand generation. An
// initialization request generates that fragment too, since ffmpeg produces
// both together. Concurrent init/media/viewer requests share one job. Canceling
// the last waiter cancels that job. The caller must close the returned file.
func (m *Manager) Segment(ctx context.Context, key string, index int, init bool) (*os.File, error) {
	m.mu.Lock()
	s, err := m.lookup(key)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(s.segments) {
		return nil, ErrNotFound
	}
	encoded, _ := json.Marshal(struct {
		Source         Source
		Size, Modified int64
		Index          int
	}{s.source, s.size, s.mtime.UnixNano(), index})
	sum := sha256.Sum256(encoded)
	id := hex.EncodeToString(sum[:])
	name := "segment.m4s"
	if init {
		name = "init.mp4"
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.stop {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		if entry := m.cache[id]; entry != nil {
			file, err := os.Open(filepath.Join(entry.dir, name))
			entry.last = time.Now()
			m.mu.Unlock()
			return file, err
		}
		m.mu.Unlock()
		err := m.do(ctx, "fragment:"+id, func(ctx context.Context) error {
			m.mu.Lock()
			ready := m.cache[id] != nil
			m.mu.Unlock()
			if ready {
				return nil
			}
			if err := unchanged(s.source.Path, s.size, s.mtime); err != nil {
				return err
			}
			dir, err := os.MkdirTemp(m.dir, "fragment-")
			if err != nil {
				return err
			}
			keep := false
			defer func() {
				if !keep {
					_ = os.RemoveAll(dir)
				}
			}()
			if err := m.render(ctx, s.source, s.segments[index], dir); err != nil {
				return err
			}
			if err := unchanged(s.source.Path, s.size, s.mtime); err != nil {
				return err
			}
			files, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			var size int64
			for _, file := range files {
				info, err := file.Info()
				if err != nil {
					return err
				}
				size += info.Size()
			}
			if size > m.cfg.MaxBytes {
				return fmt.Errorf("HLS fragment exceeds cache capacity")
			}
			m.mu.Lock()
			m.cache[id] = &cached{dir: dir, size: size, last: time.Now()}
			m.bytes += size
			m.evict(id)
			if m.bytes > m.cfg.MaxBytes {
				m.bytes -= size
				delete(m.cache, id)
				m.mu.Unlock()
				return fmt.Errorf("HLS cache capacity is occupied by open files")
			}
			m.mu.Unlock()
			keep = true
			return nil
		})
		if err != nil {
			return nil, err
		}
		// Reopen under the cache lock so eviction cannot unlink before open.
	}
}

// Release invalidates only this capability token; cached fragments remain
// reusable until the idle timeout or LRU capacity eviction.
func (m *Manager) Release(key string) {
	m.mu.Lock()
	delete(m.plays, key)
	m.mu.Unlock()
}

func (m *Manager) Close() error {
	m.cancel()
	<-m.closed
	return nil
}

// lookup and the eviction helpers require mu.
func (m *Manager) lookup(key string) (*stream, error) {
	if m.stop {
		return nil, ErrClosed
	}
	p := m.plays[key]
	if p == nil {
		return nil, ErrNotFound
	}
	if time.Since(p.last) >= m.cfg.IdleTimeout {
		delete(m.plays, key)
		return nil, ErrNotFound
	}
	p.last = time.Now()
	return p.stream, nil
}

func (m *Manager) expireOldestPlayback() {
	var oldest string
	for key, p := range m.plays {
		if oldest == "" || p.last.Before(m.plays[oldest].last) {
			oldest = key
		}
	}
	delete(m.plays, oldest)
}

func (m *Manager) evict(except string) {
	for m.bytes > m.cfg.MaxBytes {
		oldest := ""
		for id, entry := range m.cache {
			if id != except && (oldest == "" || entry.last.Before(m.cache[oldest].last)) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		if !m.remove(oldest) {
			return
		}
	}
}

func (m *Manager) remove(id string) bool {
	entry := m.cache[id]
	if err := os.RemoveAll(entry.dir); err != nil {
		return false
	}
	m.bytes -= entry.size
	delete(m.cache, id)
	return true
}

// do deduplicates work and cancels it when all request contexts go away.
func (m *Manager) do(ctx context.Context, key string, work func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.stop {
		m.mu.Unlock()
		return ErrClosed
	}
	j := m.jobs[key]
	if j != nil && j.waiters == 0 {
		// Its last request canceled it. Wait for teardown before allowing a
		// fresh request to retry, rather than joining a doomed ffmpeg job.
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-j.done:
			return m.do(ctx, key, work)
		}
	}
	if j == nil {
		jobctx, cancel := context.WithTimeout(m.ctx, m.cfg.JobTimeout)
		j = &job{done: make(chan struct{}), cancel: cancel}
		m.jobs[key] = j
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer cancel()
			select {
			case m.slots <- struct{}{}:
				if err := jobctx.Err(); err != nil {
					j.err = err
				} else {
					j.err = work(jobctx)
					if err := jobctx.Err(); err != nil {
						j.err = err
					}
				}
				<-m.slots
			case <-jobctx.Done():
				j.err = jobctx.Err()
			}
			m.mu.Lock()
			delete(m.jobs, key)
			close(j.done)
			m.mu.Unlock()
		}()
	}
	j.waiters++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		j.waiters--
		if j.waiters == 0 {
			j.cancel()
		}
		m.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-j.done:
		return j.err
	}
}

func (m *Manager) maintain() {
	tick := time.NewTicker(min(30*time.Second, m.cfg.IdleTimeout))
	defer tick.Stop()
	defer close(m.closed)
	for {
		select {
		case <-m.ctx.Done():
			m.mu.Lock()
			m.stop = true
			m.mu.Unlock()
			m.wg.Wait()
			_ = os.RemoveAll(m.dir)
			return
		case now := <-tick.C:
			m.mu.Lock()
			for key, p := range m.plays {
				if now.Sub(p.last) >= m.cfg.IdleTimeout {
					delete(m.plays, key)
				}
			}
			used := make(map[*stream]bool)
			for _, p := range m.plays {
				used[p.stream] = true
			}
			for id, s := range m.media {
				if !used[s] && now.Sub(s.last) >= m.cfg.IdleTimeout && m.jobs["probe:"+id] == nil {
					delete(m.media, id)
				}
			}
			for id, entry := range m.cache {
				if now.Sub(entry.last) >= m.cfg.IdleTimeout {
					m.remove(id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func unchanged(path string, size int64, modified time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != size || !info.ModTime().Equal(modified) {
		return ErrSourceChanged
	}
	return nil
}
