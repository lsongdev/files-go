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
	"strconv"
	"strings"
	"testing"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

func TestSubtitles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "Movies"), 0o755)
	for name, data := range map[string]string{
		"Movies/Dune.2021.mkv":     "x",
		"Movies/Dune.2021.eng.srt": "1\n00:00:01,000 --> 00:00:02,000\nHello\n",
		// "你好" in GBK
		"Movies/Dune.2021.chs.srt": "1\n00:00:01,000 --> 00:00:02,000\n\xc4\xe3\xba\xc3\n",
		"Movies/Other.mkv":         "x",
		"Movies/Other.zh.ass":      "",
	} {
		os.WriteFile(filepath.Join(root, name), []byte(data), 0o644)
	}
	cfg := &config.Config{Data: dir, FFmpeg: "ffmpeg", HWAccel: "none", Roots: []config.Root{{ID: "r", Path: root}}}
	st, _ := store.Open(filepath.Join(dir, "files.db"))
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scanner.Init(ctx)
	scanner.Scan(ctx, "r", false)
	pipeline, _ := media.NewPipeline(ctx, st, "test")
	api, apiErr := New(ctx, cfg, st, scanner, pipeline, nil, nil)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	defer api.hls.Close()
	srv := httptest.NewServer(api.Handler(http.NotFoundHandler()))
	defer srv.Close()

	movie, _ := st.ByPath(ctx, "r", "Movies/Dune.2021.mkv")
	res, _ := http.Get(srv.URL + "/api/files/" + itoa(movie.ID) + "/subtitles")
	var list struct{ Items []Track }
	json.NewDecoder(res.Body).Decode(&list)
	if len(list.Items) != 2 {
		t.Fatalf("tracks = %+v", list.Items)
	}
	var chs Track
	for _, tr := range list.Items {
		if tr.Lang == "chs" {
			chs = tr
		}
	}
	if chs.Label != "简体中文" || !chs.Default {
		t.Fatalf("chinese track = %+v", chs)
	}
	res, _ = http.Get(srv.URL + "/api/files/" + itoa(movie.ID) + "/subtitles/" + chs.ID + ".vtt")
	body, _ := io.ReadAll(res.Body)
	if !strings.HasPrefix(string(body), "WEBVTT") || !strings.Contains(string(body), "你好") {
		t.Fatalf("vtt = %q", body)
	}
	other, _ := st.ByPath(ctx, "r", "Movies/Other.mkv")
	if res, _ := http.Get(srv.URL + "/api/files/" + itoa(other.ID) + "/subtitles/" + chs.ID + ".vtt"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("another video's subtitle was served: %d", res.StatusCode)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
