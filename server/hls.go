package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	segmentSeconds = 4
	maxSessions    = 3
	sessionIdle    = 90 * time.Second
)

// HLS is what every browser can stream: Safari plays it natively (iOS has
// no other way to play a live remux or transcode, since progressive MP4
// there requires byte ranges), others through hls.js. Each playback is an
// ffmpeg process writing fMP4 segments and a growing playlist.
type hls struct {
	dir string
	mu  sync.Mutex
	all map[string]*session
}

type session struct {
	key    string // random; the segment URLs are capability URLs
	file   int64
	dir    string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	last   time.Time
}

func newHLS(ctx context.Context, dir string) *hls {
	_ = os.RemoveAll(dir) // leftovers of a previous run
	h := &hls{dir: dir, all: map[string]*session{}}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				h.stopAll(func(*session) bool { return true })
				return
			case <-ticker.C:
				h.stopAll(func(s *session) bool { return time.Since(s.last) > sessionIdle })
			}
		}
	}()
	return h
}

func (h *hls) stopAll(match func(*session) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, s := range h.all {
		if match(s) {
			h.stop(key)
		}
	}
}

// stop must be called with h.mu held.
func (h *hls) stop(key string) {
	if s, ok := h.all[key]; ok {
		s.cancel()
		delete(h.all, key)
		go func() { <-s.done; _ = os.RemoveAll(s.dir) }()
	}
}

func (h *hls) get(key string) (*session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.all[key]
	if ok {
		s.last = time.Now()
	}
	return s, ok
}

// play starts an HLS session for a media file from ?t= seconds and answers
// with its playlist URL once the first segment exists. ?transcode=1 forces
// re-encoding, the player's fallback when copied codecs do not play.
func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if f.Kind != "video" && f.Kind != "audio" {
		writeError(w, http.StatusBadRequest, "not a media file")
		return
	}
	src, err := s.scanner.Abs(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	start, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	in, out := s.ffmpegArgs(f, meta(f), r.URL.Query().Get("transcode") == "1")
	args := append([]string{"-v", "error", "-nostdin"}, in...)
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}

	key := make([]byte, 16)
	_, _ = rand.Read(key)
	sess := &session{key: hex.EncodeToString(key), file: f.ID, done: make(chan struct{}), last: time.Now()}
	sess.dir = filepath.Join(s.hls.dir, sess.key)
	if err := os.MkdirAll(sess.dir, 0o755); err != nil {
		s.fail(w, err)
		return
	}
	args = append(append(append(args, "-i", src), out...),
		"-strict", "-2", "-f", "hls", "-hls_time", strconv.Itoa(segmentSeconds), "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(sess.dir, "seg-%05d.m4s"), filepath.Join(sess.dir, "index.m3u8"))

	s.hls.mu.Lock()
	// One viewer seeking starts a new session; the old one is not needed.
	for key, other := range s.hls.all {
		if other.file == f.ID {
			s.hls.stop(key)
		}
	}
	for len(s.hls.all) >= maxSessions {
		oldest := ""
		for key, other := range s.hls.all {
			if oldest == "" || other.last.Before(s.hls.all[oldest].last) {
				oldest = key
			}
		}
		s.hls.stop(oldest)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	sess.cancel = cancel
	s.hls.all[sess.key] = sess
	s.hls.mu.Unlock()

	var stderr limitedBuffer
	cmd := exec.CommandContext(ctx, s.cfg.FFmpeg, args...)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		s.fail(w, fmt.Errorf("start ffmpeg: %w", err))
		return
	}
	go func() {
		err := cmd.Wait()
		if err != nil && ctx.Err() == nil {
			sess.err = fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
			log.Printf("hls %s/%s: %v", f.Root, f.Path, sess.err)
		}
		close(sess.done)
	}()

	playlist := filepath.Join(sess.dir, "index.m3u8")
	deadline := time.After(30 * time.Second)
	for {
		if data, err := os.ReadFile(playlist); err == nil && strings.Contains(string(data), "#EXTINF") {
			writeJSON(w, map[string]string{"url": "/api/hls/" + sess.key + "/index.m3u8"})
			return
		}
		select {
		case <-sess.done:
			if sess.err == nil {
				continue // finished before we looked: the playlist is complete
			}
			writeError(w, http.StatusUnprocessableEntity, sess.err.Error())
			return
		case <-deadline:
			writeError(w, http.StatusGatewayTimeout, "ffmpeg produced no output in time")
			return
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

var segmentName = regexp.MustCompile(`^(index\.m3u8|init\.mp4|seg-\d{5}\.m4s)$`)

// segment serves a session's playlist and segments.
func (s *Server) segment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sess, ok := s.hls.get(r.PathValue("key"))
	if !ok || !segmentName.MatchString(name) {
		writeError(w, http.StatusNotFound, "playback session ended")
		return
	}
	switch {
	case name == "index.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Content-Type", "video/mp4")
	}
	file := filepath.Join(sess.dir, name)
	if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "segment not ready")
		return
	}
	http.ServeFile(w, r, file)
}

// limitedBuffer keeps the tail of ffmpeg's stderr.
type limitedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > 8<<10 {
		b.data = b.data[len(b.data)-8<<10:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
