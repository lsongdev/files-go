package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

// titled names every text file after itself, standing in for identification.
type titled struct{}

func (titled) Match(f *store.File) bool { return f.Kind == "text" }
func (titled) Enrich(_ context.Context, f *store.File, m *media.Meta) error {
	m.Kind, m.Title = "movie", strings.TrimSuffix(f.Name, ".txt")
	return nil
}

type sse struct {
	event string
	data  json.RawMessage
}

func TestEventStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "Docs"), 0o755)
	os.WriteFile(filepath.Join(root, "Docs", "a.txt"), []byte("a"), 0o644)
	cfg := &config.Config{Data: dir, Roots: []config.Root{{ID: "r", Name: "R", Path: root}}}
	st, err := store.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scanner.Init(ctx)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	pipeline, _ := media.NewPipeline(ctx, st, "events", titled{})
	api, err := New(ctx, cfg, st, scanner, pipeline, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	srv := httptest.NewServer(api.Handler(http.NotFoundHandler()))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan sse, 64)
	go func() {
		defer close(events)
		var e sse
		lines := bufio.NewScanner(res.Body)
		for lines.Scan() {
			line := lines.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.event = line[len("event: "):]
			case strings.HasPrefix(line, "data: "):
				e.data = json.RawMessage(line[len("data: "):])
			case line == "" && e.event != "":
				events <- e
				e = sse{}
			}
		}
	}()
	// wait returns the first event of a kind that satisfies ok.
	wait := func(kind string, ok func(json.RawMessage) bool) json.RawMessage {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case e, open := <-events:
				if !open {
					t.Fatalf("stream closed waiting for %s", kind)
				}
				if e.event == kind && ok(e.data) {
					return e.data
				}
			case <-deadline:
				t.Fatalf("no %s event", kind)
			}
		}
	}
	var status struct {
		Stats   store.Stats `json:"stats"`
		Roots   []any       `json:"roots"`
		Running []any       `json:"running"`
	}
	json.Unmarshal(wait("status", func(json.RawMessage) bool { return true }), &status)
	if status.Stats.Files == 0 || len(status.Roots) != 1 || status.Running == nil {
		t.Fatalf("first status: %+v", status)
	}

	// A file appearing on disk names its folder once reconciled.
	docs, _ := st.ByPath(ctx, "r", "Docs")
	os.WriteFile(filepath.Join(root, "Docs", "b.txt"), []byte("b"), 0o644)
	if err := scanner.SyncDir(ctx, docs); err != nil {
		t.Fatal(err)
	}
	wait("changes", func(data json.RawMessage) bool {
		var c struct{ Dirs []int64 }
		json.Unmarshal(data, &c)
		return slices.Contains(c.Dirs, docs.ID)
	})
	wait("activity", func(data json.RawMessage) bool {
		return strings.Contains(string(data), `"type":"scan"`) && strings.Contains(string(data), `"path":"Docs"`)
	})

	// Identification results arrive as activity, and change their folder.
	go pipeline.Run(ctx, 1)
	wait("activity", func(data json.RawMessage) bool {
		return strings.Contains(string(data), `"type":"identified"`) && strings.Contains(string(data), `"path":"Docs/b.txt"`)
	})
	wait("changes", func(data json.RawMessage) bool {
		var c struct{ Dirs []int64 }
		json.Unmarshal(data, &c)
		return slices.Contains(c.Dirs, docs.ID)
	})

	// The activity page starts from recent activity, newest first.
	page, err := http.Get(srv.URL + "/api/activity")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ Items []Activity }
	json.NewDecoder(page.Body).Decode(&snapshot)
	page.Body.Close()
	if len(snapshot.Items) < 2 || snapshot.Items[0].ID < snapshot.Items[len(snapshot.Items)-1].ID {
		t.Fatalf("activity snapshot: %+v", snapshot.Items)
	}
}

func TestHubCoalescesAndDropsSlowClients(t *testing.T) {
	h := newHub()
	ch := h.subscribe()
	ids := make([]int64, maxChangeIDs+1)
	for i := range ids {
		ids[i] = int64(i)
	}
	h.change(store.Change{Files: ids})
	h.change(store.Change{Dirs: []int64{1}})
	dirs, files, resync, _, _, dirty, listening := h.pending()
	if !resync || len(dirs)+len(files) != 0 || !dirty || !listening {
		t.Fatalf("too many changes should ask for a resync: %v %v %v", dirs, files, resync)
	}
	if _, _, resync, _, _, _, _ := h.pending(); resync {
		t.Fatal("resync is sent once")
	}
	for range clientBuffer + 1 {
		h.broadcast("status", map[string]int{"n": 1})
	}
	for range clientBuffer {
		<-ch
	}
	if _, open := <-ch; open {
		t.Fatal("a client that cannot keep up should be disconnected")
	}
	h.unsubscribe(ch) // already dropped: no double close
	for range recentSize + 5 {
		h.outcome(media.Outcome{File: store.File{Root: "r", Path: "x"}, Kind: "movie", Title: "X"})
	}
	if items := h.snapshot(); len(items) != recentSize || items[0].ID != int64(recentSize+5) {
		t.Fatalf("recent activity is bounded and newest first: %d, %+v", len(items), items[0])
	}
}
