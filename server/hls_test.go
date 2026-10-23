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
	srv := httptest.NewServer(New(ctx, cfg, st, scanner, pipeline, nil).Handler(http.NotFoundHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+itoa(clip.ID)+"/play?transcode=1", nil)
	req.SetBasicAuth("", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("play: %v %s", err, body)
	}
	var started struct{ URL string }
	json.NewDecoder(res.Body).Decode(&started)

	// Segments need no credentials: the session key is the capability.
	get := func(path string) string {
		res, err := http.Get(srv.URL + path)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v %v", path, err, res.Status)
		}
		body, _ := io.ReadAll(res.Body)
		return string(body)
	}
	playlist := get(started.URL)
	if !strings.Contains(playlist, "#EXT-X-MAP:URI=\"init.mp4\"") || !strings.Contains(playlist, "seg-00000.m4s") {
		t.Fatalf("playlist = %s", playlist)
	}
	base := strings.TrimSuffix(started.URL, "index.m3u8")
	if init := get(base + "init.mp4"); !strings.Contains(init, "ftyp") || !strings.Contains(init, "avc1") {
		t.Fatal("init segment is not H.264 fMP4")
	}
	if seg := get(base + "seg-00000.m4s"); len(seg) < 1000 {
		t.Fatalf("segment is %d bytes", len(seg))
	}
	if res, _ := http.Get(srv.URL + "/api/hls/0123456789abcdef0123456789abcdef/index.m3u8"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session = %d", res.StatusCode)
	}
	if res, _ := http.Get(srv.URL + base + "../../files/" + itoa(clip.ID)); res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusNotFound {
		t.Fatalf("path escape = %d", res.StatusCode)
	}
}
