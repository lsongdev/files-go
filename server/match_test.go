package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/store"
)

type playableFixture struct{}

func (playableFixture) Match(f *store.File) bool { return f.Kind == "video" }
func (playableFixture) Enrich(_ context.Context, _ *store.File, m *media.Meta) error {
	m.Kind, m.Video = "video", "h264"
	return nil
}

func TestProviderCandidatesAndSharedDirectoryMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jav":
			if r.URL.Query().Get("q") == "manual" {
				w.Write([]byte(`{"data":[{"id":"manual","title":"Manual"}]}`))
			} else {
				w.Write([]byte(`{"data":[{"id":"auto","title":"CAWD-549 Automatic"}]}`))
			}
		case "/jav/auto":
			w.Write([]byte(`{"data":{"id":"auto","title":"Automatic","date":"2024-01-01"}}`))
		case "/jav/manual":
			w.Write([]byte(`{"data":{"id":"manual","title":"Manual","date":"2025-01-01"}}`))
		default:
			t.Errorf("unexpected catalog request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer catalog.Close()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, name := range []string{"CAWD-549/CAWD-549-C.mkv", "CAWD-549/CAWD-549-D.mkv"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("video"), 0600)
	}
	st, err := store.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tpdb, err := theporndb.New(theporndb.Options{Token: "test", BaseURL: catalog.URL, Cache: media.ThePornDBCache{Store: st}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Data: dir, Roots: []config.Root{{ID: "r", Path: root, Type: "jav"}}}
	scanner := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	hint := func(store.File) (string, string) { return "jav", "" }
	library := &media.Library{Store: st, Path: scanner.Abs, Hint: hint}
	pipeline, _ := media.NewPipeline(ctx, st, "matches", playableFixture{}, library.Videos(), library.Folders(), &media.JAV{Client: tpdb, Store: st, Hint: hint})
	done := make(chan struct{})
	go func() { pipeline.Run(ctx, 3); close(done) }()
	defer func() { cancel(); <-done }()
	settle := func() {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			stats, err := st.Stats(ctx, pipeline.Version)
			if err != nil {
				t.Fatal(err)
			}
			if stats.Pending == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("pending: %+v", stats)
			}
		}
	}
	settle()
	api, err := New(ctx, cfg, st, scanner, pipeline, nil, tpdb)
	if err != nil {
		t.Fatal(err)
	}
	defer api.hls.Close()
	handler := api.Handler(http.NotFoundHandler())
	folder, _ := st.ByPath(ctx, "r", "CAWD-549")
	request := func(method string, id int64, suffix, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, fmt.Sprintf("/api/files/%d/match%s", id, suffix), bytes.NewBufferString(body)))
		return w
	}
	w := request("GET", folder.ID, "?provider=theporndb&type=jav&q=manual", "")
	var candidates struct{ Items []Candidate }
	if err := json.Unmarshal(w.Body.Bytes(), &candidates); w.Code != 200 || err != nil || len(candidates.Items) != 1 || candidates.Items[0].Match.Provider != "theporndb" {
		t.Fatalf("candidates: %d %s", w.Code, w.Body)
	}
	if w := request("PUT", folder.ID, "", `{"tmdb":"movie/1"}`); w.Code != 400 {
		t.Fatalf("legacy body accepted: %d", w.Code)
	}
	if w := request("PUT", folder.ID, "", `{"match":{"provider":"theporndb","type":"jav","id":"manual"}}`); w.Code != 200 {
		t.Fatalf("match: %d %s", w.Code, w.Body)
	}
	settle()
	for _, name := range []string{"CAWD-549", "CAWD-549/CAWD-549-C.mkv", "CAWD-549/CAWD-549-D.mkv"} {
		f, _ := st.ByPath(ctx, "r", name)
		m := meta(f)
		if m.Match == nil || m.Match.ID != "manual" || !m.Locked || m.Title != "Manual" {
			t.Fatalf("shared choice %s: %+v", name, m)
		}
	}
	file, _ := st.ByPath(ctx, "r", "CAWD-549/CAWD-549-C.mkv")
	if w := request("DELETE", file.ID, "", ""); w.Code != 200 {
		t.Fatalf("unlock: %d %s", w.Code, w.Body)
	}
	settle()
	for _, name := range []string{"CAWD-549", "CAWD-549/CAWD-549-D.mkv"} {
		f, _ := st.ByPath(ctx, "r", name)
		m := meta(f)
		if m.Match == nil || m.Match.ID != "auto" || m.Locked {
			t.Fatalf("shared unlock %s: %+v", name, m)
		}
	}
}
