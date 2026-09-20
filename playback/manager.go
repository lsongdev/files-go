package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

var safeSessionFile = regexp.MustCompile(`^(master\.m3u8|segment-[0-9]{5}\.ts)$`)

type Result struct {
	Mode     string `json:"mode"`
	Strategy Mode   `json:"strategy,omitempty"`
	URL      string `json:"url"`
}

type session struct {
	dir        string
	cancel     context.CancelFunc
	lastAccess time.Time
	err        error
}

type Manager struct {
	ctx       context.Context
	catalog   *catalog.Catalog
	storages  *storage.Registry
	ffmpeg    string
	cacheDir  string
	semaphore chan struct{}
	mu        sync.Mutex
	sessions  map[string]*session
}

func NewManager(ctx context.Context, catalogDB *catalog.Catalog, storages *storage.Registry, ffmpeg, cacheDir string, concurrency int) *Manager {
	if concurrency <= 0 {
		concurrency = 2
	}
	manager := &Manager{ctx: ctx, catalog: catalogDB, storages: storages, ffmpeg: ffmpeg,
		cacheDir: filepath.Join(cacheDir, "transcodes"), semaphore: make(chan struct{}, concurrency), sessions: make(map[string]*session)}
	go manager.collect(ctx)
	return manager
}

func (m *Manager) Start(ctx context.Context, entryID string, capabilities Capabilities) (Result, error) {
	entry, err := m.catalog.Entry(ctx, entryID)
	if err != nil {
		return Result{}, err
	}
	media, err := m.catalog.MediaForEntry(ctx, entryID)
	if err != nil {
		return Result{}, err
	}
	var details struct {
		Embedded struct {
			Container  string `json:"container"`
			VideoCodec string `json:"videoCodec"`
			AudioCodec string `json:"audioCodec"`
		} `json:"embedded"`
	}
	if err := json.Unmarshal(media.Data, &details); err != nil {
		return Result{}, err
	}
	technical := model.ParsedMedia{Container: details.Embedded.Container, VideoCodec: details.Embedded.VideoCodec, AudioCodec: details.Embedded.AudioCodec}
	mode := Decide(technical, capabilities)
	if mode == ModeDirect {
		return Result{Mode: string(mode), URL: "/api/v1/entries/" + entryID + "/content"}, nil
	}
	backend, ok := m.storages.Get(entry.StorageID)
	if !ok {
		return Result{}, storage.ErrNotFound
	}
	native, ok := backend.(storage.NativePather)
	if !ok {
		return Result{}, storage.ErrUnsupported
	}
	input, err := native.NativePath(ctx, entry.Path)
	if err != nil {
		return Result{}, err
	}
	id := uuid.Must(uuid.NewV7()).String()
	dir := filepath.Join(m.cacheDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Result{}, err
	}
	processCtx, cancel := context.WithTimeout(m.ctx, 12*time.Hour)
	s := &session{dir: dir, cancel: cancel, lastAccess: time.Now()}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go m.run(processCtx, id, s, input, mode)
	result := Result{Mode: "hls", Strategy: mode, URL: "/api/v1/playback/sessions/" + id + "/master.m3u8"}
	manifest := filepath.Join(dir, "master.m3u8")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Stop(id)
			return Result{}, ctx.Err()
		case <-timeout.C:
			return result, nil
		case <-ticker.C:
			if info, statErr := os.Stat(manifest); statErr == nil && info.Size() > 0 {
				return result, nil
			}
			m.mu.Lock()
			processErr := s.err
			m.mu.Unlock()
			if processErr != nil {
				m.Stop(id)
				return Result{}, processErr
			}
		}
	}
}

func (m *Manager) run(ctx context.Context, id string, s *session, input string, mode Mode) {
	select {
	case m.semaphore <- struct{}{}:
	case <-ctx.Done():
		m.setError(id, ctx.Err())
		return
	}
	defer func() { <-m.semaphore }()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-i", input}
	if mode == ModeRemux {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?", "-c", "copy")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-crf", "22", "-force_key_frames", "expr:gte(t,n_forced*2)", "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, "-f", "hls", "-hls_time", "2", "-hls_list_size", "0", "-hls_segment_filename", filepath.Join(s.dir, "segment-%05d.ts"), filepath.Join(s.dir, "master.m3u8"))
	command := exec.CommandContext(ctx, m.ffmpeg, args...)
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		m.setError(id, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String())))
	}
}

func (m *Manager) File(sessionID, name string) (string, error) {
	if !safeSessionFile.MatchString(name) {
		return "", os.ErrNotExist
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return "", os.ErrNotExist
	}
	s.lastAccess = time.Now()
	if s.err != nil {
		return "", s.err
	}
	return filepath.Join(s.dir, name), nil
}

func (m *Manager) Stop(sessionID string) bool {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	if ok {
		s.cancel()
		_ = os.RemoveAll(s.dir)
	}
	return ok
}

func (m *Manager) setError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		s.err = err
	}
}

func (m *Manager) collect(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.removeAll()
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				if now.Sub(s.lastAccess) > 30*time.Minute {
					s.cancel()
					_ = os.RemoveAll(s.dir)
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) removeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		s.cancel()
		_ = os.RemoveAll(s.dir)
		delete(m.sessions, id)
	}
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 32 << 10
	b.data = append(b.data, p...)
	if len(b.data) > limit {
		b.data = b.data[len(b.data)-limit:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.data) }
