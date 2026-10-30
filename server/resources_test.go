package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/source"
	"github.com/lsongdev/files-go/store"
)

func TestSharedJobsSurviveWaiterCancellation(t *testing.T) {
	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	var group jobs
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	run := func(ctx context.Context) error {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	first, cancel := context.WithCancel(lifetime)
	done := make(chan error, 1)
	go func() { done <- group.do(first, lifetime, "key", time.Second, run) }()
	<-started
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	group.mu.Lock()
	task := group.active["key"]
	group.mu.Unlock()
	wait, cancelWait := context.WithTimeout(lifetime, 20*time.Millisecond)
	defer cancelWait()
	if e := group.do(wait, lifetime, "key", time.Second, run); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("waiter: %v", e)
	}
	close(release)
	<-task.done
	if task.err != nil {
		t.Fatal(task.err)
	}
	if calls.Load() != 1 {
		t.Fatal("shared job ran twice")
	}
}
func TestOversizeImageIsRejectedBeforeDecode(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	data := b.Bytes()
	// Change IHDR dimensions and checksum without allocating a giant image.
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, e := scale(bytes.NewReader(data), "png"); !errors.Is(e, source.ErrLimit) {
		t.Fatalf("oversize decode: %v", e)
	}
}
func TestRootBoundaryAndOversizeSubtitle(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	dir := t.TempDir()
	root := filepath.Join(dir, "media")
	os.Mkdir(root, 0700)
	for n, b := range map[string]string{"Movie.mkv": "video", "Movie.srt": "1\n00:00:01,000 --> 00:00:02,000\nhello\n", "picture.png": "image"} {
		os.WriteFile(filepath.Join(root, n), []byte(b), 0600)
	}
	st, e := store.Open(filepath.Join(dir, "files.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	cfg := &config.Config{Data: dir, FFmpeg: "ffmpeg", Roots: []config.Root{{ID: "r", Path: root}}}
	scan := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scan.Scan(ctx, "r", false)
	pipeline, _ := media.NewPipeline(ctx, st, "limits")
	api, e := New(ctx, cfg, st, scan, pipeline, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	handler := api.Handler(http.NotFoundHandler())
	video, _ := st.ByPath(ctx, "r", "Movie.mkv")
	sub, _ := st.ByPath(ctx, "r", "Movie.srt")
	os.Truncate(filepath.Join(root, "Movie.srt"), 16<<20+1)
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := request("/api/files/" + itoa(video.ID) + "/subtitles/f" + itoa(sub.ID) + ".vtt"); w.Code != 413 {
		t.Fatalf("large subtitle: %d %s", w.Code, w.Body)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("SECRET"), 0600)
	for _, name := range []string{"Movie.mkv", "Movie.srt", "picture.png"} {
		os.Remove(filepath.Join(root, name))
		if e := os.Symlink(outside, filepath.Join(root, name)); e != nil {
			t.Skip(e)
		}
	}
	for _, path := range []string{"/api/files/" + itoa(video.ID) + "/raw", "/api/files/" + itoa(video.ID) + "/play", "/api/files/" + itoa(video.ID) + "/subtitles/f" + itoa(sub.ID) + ".vtt"} {
		w := request(path)
		if w.Code == 200 || bytes.Contains(w.Body.Bytes(), []byte("SECRET")) {
			t.Fatalf("root escape: %s %d %s", path, w.Code, w.Body)
		}
	}
	imageFile, _ := st.ByPath(ctx, "r", "picture.png")
	if _, e := api.thumbs.Get(ctx, imageFile, media.Meta{}, "self", "w500"); e == nil {
		t.Fatal("thumbnail escaped root")
	}
}
func TestAtomicCacheConcurrentWriters(t *testing.T) {
	name := filepath.Join(t.TempDir(), "cache")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := writeFile(name, bytes.Repeat([]byte{byte(i)}, 4096)); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	b, e := os.ReadFile(name)
	if e != nil || len(b) != 4096 {
		t.Fatalf("cache: %d %v", len(b), e)
	}
	if !bytes.Equal(b, bytes.Repeat(b[:1], len(b))) {
		t.Fatal("torn cache entry")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(name), ".cache-*"))
	if len(files) > 0 {
		t.Fatal("temporary files leaked")
	}
}
func TestSubtitleEncodingPrefix(t *testing.T) {
	for _, prefix := range [][]byte{[]byte("hello"), append([]byte("hello"), 0xe4, 0xbd)} {
		if !bytes.Equal(completePrefix(prefix), []byte("hello")) {
			t.Fatalf("partial UTF-8 tail: %q", completePrefix(prefix))
		}
	}
	if !bytes.Equal(completePrefix([]byte{0xc4, 0xe3, 0xba, 0xc3}), []byte{0xc4, 0xe3, 0xba, 0xc3}) {
		t.Fatal("GBK prefix was rewritten")
	}
}
func TestBoundedStreamAndAtomicFailure(t *testing.T) {
	name := filepath.Join(t.TempDir(), "output")
	e := source.Atomic(name, func(w io.Writer) error {
		_, e := (&source.Writer{W: w, Remaining: 4}).Write([]byte("too big"))
		return e
	})
	if !errors.Is(e, source.ErrLimit) {
		t.Fatal(e)
	}
	if _, e := os.Stat(name); !os.IsNotExist(e) {
		t.Fatal("failed output published")
	}
}

func TestSubtitleRequestsShareOneConversion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor-based subprocesses require Unix")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	dir := t.TempDir()
	root := filepath.Join(dir, "media")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "Movie.mkv"), []byte("video"), 0600)
	os.WriteFile(filepath.Join(root, "Movie.srt"), []byte("subtitle"), 0600)
	trace := filepath.Join(dir, "calls")
	t.Setenv("SUB_TRACE", trace)
	bin := filepath.Join(dir, "ffmpeg")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf x >> \"$SUB_TRACE\"\nsleep 0.1\nprintf 'WEBVTT\\n\\n00:00:01.000 --> 00:00:02.000\\nhello\\n'\n"), 0700)
	cfg := &config.Config{Data: dir, FFmpeg: bin, HWAccel: "none", Roots: []config.Root{{ID: "r", Path: root}}}
	st, e := store.Open(filepath.Join(dir, "files.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	scan := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scan.Scan(ctx, "r", false)
	pipeline, _ := media.NewPipeline(ctx, st, "subtitle-jobs")
	api, e := New(ctx, cfg, st, scan, pipeline, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	handler := api.Handler(http.NotFoundHandler())
	video, _ := st.ByPath(ctx, "r", "Movie.mkv")
	sub, _ := st.ByPath(ctx, "r", "Movie.srt")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/files/"+itoa(video.ID)+"/subtitles/f"+itoa(sub.ID)+".vtt", nil))
			if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("WEBVTT")) {
				t.Errorf("subtitle: %d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	b, e := os.ReadFile(trace)
	if e != nil || string(b) != "x" {
		t.Fatalf("duplicate conversions: %q %v", b, e)
	}
}
func TestThumbnailFailureExpiresAndRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor-based subprocesses require Unix")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	dir := t.TempDir()
	root := filepath.Join(dir, "media")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "bad.png"), []byte("invalid image"), 0600)
	trace, fail := filepath.Join(dir, "calls"), filepath.Join(dir, "fail")
	os.WriteFile(fail, []byte("fail"), 0600)
	t.Setenv("THUMB_TRACE", trace)
	t.Setenv("THUMB_FAIL", fail)
	bin := filepath.Join(dir, "ffmpeg")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf x >> \"$THUMB_TRACE\"\nif [ -f \"$THUMB_FAIL\" ]; then exit 1; fi\nprintf 'thumbnail'\n"), 0700)
	st, e := store.Open(filepath.Join(dir, "files.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	cfg := &config.Config{Data: dir, FFmpeg: bin, Roots: []config.Root{{ID: "r", Path: root}}}
	scan := store.NewScanner(st, cfg.Roots, nil, media.Classify)
	scan.Scan(ctx, "r", false)
	f, _ := st.ByPath(ctx, "r", "bad.png")
	thumbs := NewThumbs(ctx, cfg, st, scan, nil)
	for i := 0; i < 2; i++ {
		if _, e := thumbs.Get(ctx, f, media.Meta{}, "self", "w500"); !errors.Is(e, errNoThumb) {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(trace)
	if string(b) != "x" {
		t.Fatal("negative cache did not suppress retries")
	}
	failures, _ := filepath.Glob(filepath.Join(dir, "cache", "*", "*.err"))
	if len(failures) != 1 {
		t.Fatalf("failure cache: %v", failures)
	}
	os.WriteFile(failures[0], []byte(`{"Until":0}`), 0600)
	os.Remove(fail)
	if _, e := thumbs.Get(ctx, f, media.Meta{}, "self", "w500"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(trace)
	if string(b) != "xx" {
		t.Fatal("expired negative cache did not retry")
	}
}

func TestSharedJobsCloseJoinsGeneration(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	var j jobs
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- j.do(context.Background(), lifetime, "work", time.Minute, func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() })
	}()
	<-started
	cancel()
	j.close()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := j.do(context.Background(), context.Background(), "new", time.Minute, func(context.Context) error { t.Error("closed group started new job"); return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
