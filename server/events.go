package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

// Live updates use server-sent events. Events are hints: the browser re-reads
// what it shows through the regular API, so a lost event or a reconnect only
// costs a refresh. Index changes are coalesced into one event per second.
const (
	eventInterval = time.Second
	statsInterval = 3 * time.Second // counting the index takes a few hundred ms
	maxChangeIDs  = 2000            // more changes ask clients to resync
	recentSize    = 300             // activity kept for the activity page
	activityBurst = 50              // newest activity sent per event
	clientBuffer  = 16              // events queued per client before it is dropped
)

// Activity is one entry of the live activity feed.
type Activity struct {
	ID     int64  `json:"id"`
	Time   int64  `json:"time"` // unix ms
	Type   string `json:"type"` // scan, identified, processed, review, failed, retry
	Root   string `json:"root"`
	Path   string `json:"path"`
	FileID int64  `json:"fileId,omitempty"`
	Text   string `json:"text,omitempty"` // a title, counts or an error
}

type hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	dirs    map[int64]struct{}
	files   map[int64]struct{}
	resync  bool
	recent  []Activity
	seq     int64
	sent    int64 // last activity broadcast
	dirty   bool  // index counts may have changed
}

func newHub() *hub {
	return &hub{clients: map[chan []byte]struct{}{}, dirs: map[int64]struct{}{}, files: map[int64]struct{}{}}
}

func (h *hub) subscribe() chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, clientBuffer)
	h.clients[ch] = struct{}{}
	return ch
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

// change records committed index updates; it never blocks the writer.
func (h *hub) change(c store.Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirty = true
	if len(h.clients) > 0 && !h.resync {
		for _, id := range c.Dirs {
			h.dirs[id] = struct{}{}
		}
		for _, id := range c.Files {
			h.files[id] = struct{}{}
		}
		if len(h.dirs)+len(h.files) > maxChangeIDs {
			h.resync = true
			clear(h.dirs)
			clear(h.files)
		}
	}
	if c.Root != "" {
		h.add(Activity{Type: "scan", Root: c.Root, Path: c.Path, FileID: firstID(c.Dirs),
			Text: fmt.Sprintf("新增 %d · 删除 %d · 更新 %d", c.Added, c.Removed, c.Updated)})
	}
}

// outcome records a file a worker finished.
func (h *hub) outcome(o media.Outcome) {
	a := Activity{Type: "processed", Root: o.File.Root, Path: o.File.Path, FileID: o.File.ID, Text: o.Title}
	switch {
	case o.Retry:
		a.Type, a.Text = "retry", o.Err
	case o.Err != "":
		a.Type, a.Text = "failed", o.Err
	case o.Review != "":
		a.Type = "review"
	case o.Kind != "" && o.Title != "":
		a.Type = "identified"
	}
	if a.Text == "" && o.File.Path != "" { // the root itself has no name of its own
		a.Text = path.Base(o.File.Path)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirty = true
	h.add(a)
}

func (h *hub) add(a Activity) {
	h.seq++
	a.ID, a.Time = h.seq, time.Now().UnixMilli()
	if len(h.recent) == recentSize {
		h.recent = append(h.recent[:0], h.recent[1:]...)
	}
	h.recent = append(h.recent, a)
}

// snapshot returns recent activity, newest first.
func (h *hub) snapshot() []Activity {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := make([]Activity, len(h.recent))
	for i, a := range h.recent {
		items[len(items)-1-i] = a
	}
	return items
}

// broadcast queues an event for every client; a client too slow to keep up
// is disconnected, and reconnects with a full refresh.
func (h *hub) broadcast(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(payload) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// pending takes the coalesced changes and the activity not yet broadcast.
func (h *hub) pending() (dirs, files []int64, resync bool, activity []Activity, skipped int, dirty, listening bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.dirs {
		dirs = append(dirs, id)
	}
	for id := range h.files {
		files = append(files, id)
	}
	clear(h.dirs)
	clear(h.files)
	resync, h.resync = h.resync, false
	for i := len(h.recent) - 1; i >= 0 && h.recent[i].ID > h.sent; i-- {
		if len(activity) < activityBurst {
			activity = append(activity, h.recent[i])
		} else {
			skipped++
		}
	}
	h.sent = h.seq
	dirty, h.dirty = h.dirty, false
	return dirs, files, resync, activity, skipped, dirty, len(h.clients) > 0
}

func firstID(ids []int64) int64 {
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// runEvents publishes coalesced changes, activity and status once a second.
func (s *Server) runEvents() {
	tick := time.NewTicker(eventInterval)
	defer tick.Stop()
	var last []byte
	var countedAt time.Time
	stale := true
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		dirs, files, resync, activity, skipped, dirty, listening := s.events.pending()
		stale = stale || dirty
		if !listening {
			continue
		}
		if resync || len(dirs)+len(files) > 0 {
			s.events.broadcast("changes", map[string]any{"dirs": dirs, "files": files, "resync": resync})
		}
		if len(activity) > 0 {
			s.events.broadcast("activity", map[string]any{"items": activity, "skipped": skipped})
		}
		if stale && time.Since(countedAt) >= statsInterval {
			if s.countStats(s.ctx) == nil {
				countedAt, stale = time.Now(), false
			}
		}
		status, err := json.Marshal(s.statusView())
		if err == nil && !bytes.Equal(status, last) {
			last = status
			s.events.broadcast("status", json.RawMessage(status))
		}
	}
}

// freshStats recounts the index when the cached counts are older than maxAge.
func (s *Server) freshStats(ctx context.Context, maxAge time.Duration) error {
	s.statsMu.Lock()
	age := time.Since(s.statsAt)
	s.statsMu.Unlock()
	if age <= maxAge {
		return nil
	}
	return s.countStats(ctx)
}

// countStats refreshes the cached index counts.
func (s *Server) countStats(ctx context.Context) error {
	stats, err := s.store.Stats(ctx, s.pipeline.Version)
	if err != nil {
		return err
	}
	s.statsMu.Lock()
	s.stats, s.statsAt = stats, time.Now()
	s.statsMu.Unlock()
	return nil
}

// RunningView is a file a worker is enriching.
type RunningView struct {
	ID   int64  `json:"id"`
	Root string `json:"root"`
	Path string `json:"path"`
}

func (s *Server) statusView() map[string]any {
	running := []RunningView{}
	for _, f := range s.pipeline.Running() {
		running = append(running, RunningView{f.ID, f.Root, f.Path})
	}
	s.statsMu.Lock()
	stats := s.stats
	s.statsMu.Unlock()
	return map[string]any{"roots": s.scanner.Status(), "stats": stats, "running": running}
}

// events streams live updates until the client leaves or the server stops.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // let reverse proxies pass events through
	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)
	// The first status arrives at once; changes start from here, so the
	// client refreshes what it shows when it (re)connects.
	if err := s.freshStats(r.Context(), 5*time.Second); err != nil {
		s.fail(w, err)
		return
	}
	status, _ := json.Marshal(s.statusView())
	fmt.Fprintf(w, "retry: 3000\nevent: status\ndata: %s\n\n", status)
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return // too slow; the browser reconnects
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// activity returns recent activity, newest first, and the current status.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": s.events.snapshot(), "status": s.statusView()})
}
