package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

func TestAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "Pictures"), 0o755)
	os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello world"), 0o644)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	img.Set(5, 5, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(filepath.Join(root, "Pictures", "sunset.png"), buf.Bytes(), 0o644)

	cfg := &config.Config{Data: dir, FFmpeg: "ffmpeg", Roots: []config.Root{{ID: "home", Name: "Home", Path: root}}, Auth: config.Auth{Password: "secret"}}
	st, err := store.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, config.DefaultExclude, media.Classify)
	scanner.Init(ctx)
	if err := scanner.Scan(ctx, "home", false); err != nil {
		t.Fatal(err)
	}
	pipeline, _ := media.NewPipeline(ctx, st, "test", media.Photo{Path: scanner.Abs})
	pending, _ := st.Pending(ctx, pipeline.Version, store.Cursor{}, 100)
	for _, f := range pending {
		if err := pipeline.Rematch(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	api, apiErr := New(ctx, cfg, st, scanner, pipeline, nil)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	defer api.hls.Close()
	srv := httptest.NewServer(api.Handler(http.NotFoundHandler()))
	defer srv.Close()

	get := func(path string, headers ...string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.SetBasicAuth("", "secret")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	decode := func(res *http.Response, v any) {
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("status %d: %s", res.StatusCode, body)
		}
		json.NewDecoder(res.Body).Decode(v)
	}

	if res, _ := http.Get(srv.URL + "/api/roots"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request = %d", res.StatusCode)
	}
	var roots []store.RootStatus
	decode(get("/api/roots"), &roots)
	if len(roots) != 1 || roots[0].FileID == 0 {
		t.Fatalf("roots = %+v", roots)
	}
	var list struct{ Items []View }
	decode(get(fmt.Sprintf("/api/files/%d/children", roots[0].FileID)), &list)
	if len(list.Items) != 2 || !list.Items[0].Dir || list.Items[1].Name != "readme.txt" {
		t.Fatalf("children = %+v", list.Items)
	}
	// Browsing must not reconcile the media filesystem, even if it changes or
	// disappears. Only an explicit/background scan is allowed to change the index.
	os.WriteFile(filepath.Join(root, "unscanned.txt"), []byte("new"), 0o644)
	os.Chtimes(root, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	decode(get(fmt.Sprintf("/api/files/%d/children", roots[0].FileID)), &list)
	if len(list.Items) != 2 {
		t.Fatal("browsing unexpectedly scanned the directory")
	}
	moved := root + "-offline"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	decode(get(fmt.Sprintf("/api/files/%d/children", roots[0].FileID)), &list)
	if len(list.Items) != 2 {
		t.Fatal("offline browsing changed the index")
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	readme := list.Items[1]
	res := get(fmt.Sprintf("/api/files/%d/raw", readme.ID), "Range", "bytes=0-4")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusPartialContent || string(body) != "hello" {
		t.Fatalf("range = %d %q", res.StatusCode, body)
	}

	decode(get("/api/search?q=sunset"), &list)
	if len(list.Items) != 1 || !list.Items[0].Thumb {
		t.Fatalf("search = %+v", list.Items)
	}
	var m media.Meta
	json.Unmarshal(list.Items[0].Meta, &m)
	if m.Kind != "photo" || m.Width != 1200 {
		t.Fatalf("photo meta = %+v", m)
	}
	res = get(fmt.Sprintf("/api/files/%d/thumb", list.Items[0].ID))
	thumb, _, err := image.Decode(res.Body)
	if err != nil || thumb.Bounds().Dx() != thumbSize || thumb.Bounds().Dy() != 320 {
		t.Fatalf("thumbnail: %v %v", err, thumb)
	}

	var issues struct{ Items []View }
	decode(get("/api/issues?type=error"), &issues)
	if len(issues.Items) != 0 {
		t.Fatalf("unexpected failures: %+v", issues.Items)
	}

	var detail struct {
		File      View
		Ancestors []View
	}
	decode(get(fmt.Sprintf("/api/files/%d", list.Items[0].ID)), &detail)
	if len(detail.Ancestors) != 2 || detail.Ancestors[1].Name != "Pictures" {
		t.Fatalf("ancestors = %+v", detail.Ancestors)
	}
}
