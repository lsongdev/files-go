package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

func TestHLS(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(root, 0o755)
	// A 10 s MPEG-4 Part 2 + MP3 AVI: neither codec is browser friendly.
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10", "-c:v", "mpeg4", "-c:a", "libmp3lame", filepath.Join(root, "clip.avi")).CombinedOutput(); err != nil {
		t.Skipf("cannot create test clip: %v %s", err, out)
	}
	cfg := &config.Config{Data: dir, FFmpeg: "ffmpeg", HWAccel: "none", Roots: []config.Root{{ID: "r", Path: root}}, Auth: config.Auth{Password: "pw"}}
	st, _ := store.Open(filepath.Join(dir, "files.db"))
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scanner.Init(ctx)
	scanner.Scan(ctx, "r", false)
	pipeline, _ := media.NewPipeline(ctx, st, "test", &media.Probe{Bin: "ffprobe", Path: scanner.Abs})
	clip, _ := st.ByPath(ctx, "r", "clip.avi")
	if err := pipeline.Rematch(ctx, clip); err != nil {
		t.Fatal(err)
	}
	api, apiErr := New(ctx, cfg, st, scanner, pipeline, nil)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	defer api.hls.Close()
	srv := httptest.NewServer(api.Handler(http.NotFoundHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+itoa(clip.ID)+"/play?transcode=1", nil)
	req.SetBasicAuth("", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("play: %v %s", err, body)
	}
	var started struct{ URL string }
	json.NewDecoder(res.Body).Decode(&started)

	// Segments need no credentials: the session key is the capability.
	get := func(path string) string {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v", path, res.Status)
		}
		body, _ := io.ReadAll(res.Body)
		return string(body)
	}
	playlist := get(started.URL)
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(playlist, "#EXT-X-ENDLIST") || !strings.Contains(playlist, "#EXT-X-MAP:URI=\"init-00000.mp4\"") || !strings.Contains(playlist, "seg-00000.m4s") {
		t.Fatalf("playlist = %s", playlist)
	}
	base := strings.TrimSuffix(started.URL, "index.m3u8")
	if init := get(base + "init-00001.mp4"); !strings.Contains(init, "ftyp") || !strings.Contains(init, "avc1") {
		t.Fatal("init segment is not H.264 fMP4")
	}
	if seg := get(base + "seg-00001.m4s"); len(seg) < 1000 {
		t.Fatalf("segment is %d bytes", len(seg))
	}
	if res, _ := http.Get(srv.URL + "/api/hls/0123456789abcdef0123456789abcdef/index.m3u8"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session = %d", res.StatusCode)
	}
	if res, _ := http.Get(srv.URL + base + "../../files/" + itoa(clip.ID)); res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusNotFound {
		t.Fatalf("path escape = %d", res.StatusCode)
	}
	for _, name := range []string{"init-00001.m4s", "seg-00001.mp4", "seg-99999.m4s"} {
		res, err := http.Get(srv.URL + base + name)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("invalid resource %s = %d", name, res.StatusCode)
		}
	}
	ranged, _ := http.NewRequest(http.MethodGet, srv.URL+base+"seg-00001.m4s", nil)
	ranged.Header.Set("Range", "bytes=0-31")
	res, err = http.DefaultClient.Do(ranged)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusPartialContent || len(body) != 32 {
		t.Fatalf("fragment Range: %s, %d bytes, %v", res.Status, len(body), err)
	}
	release, _ := http.NewRequest(http.MethodDelete, srv.URL+strings.TrimSuffix(base, "/"), nil)
	res, err = http.DefaultClient.Do(release)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("release: %s", res.Status)
	}
	res, err = http.Get(srv.URL + started.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("released playback: %s", res.Status)
	}
}
