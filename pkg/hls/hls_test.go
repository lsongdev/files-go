package hls

import (
	"context"
	"errors"
	filesource "github.com/lsongdev/files-go/pkg/source"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func manager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	cfg.CacheDir = t.TempDir()
	m, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func fixture(t *testing.T, audio bool) string {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	path := filepath.Join(t.TempDir(), "source.mp4")
	args := []string{"-v", "error", "-f", "lavfi"}
	if audio {
		args = append(args, "-i", "sine=duration=20.04:sample_rate=48000", "-c:a", "aac")
	} else {
		args = append(args, "-i", "testsrc2=size=320x240:rate=25:duration=20.04", "-f", "lavfi", "-i", "sine=duration=20.04:sample_rate=48000", "-c:v", "libx264", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-bf", "3", "-c:a", "aac", "-shortest")
	}
	if out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	return path
}

func TestLazyVOD(t *testing.T) {
	source := fixture(t, false)
	for _, copyVideo := range []bool{true, false} {
		t.Run(strconv.FormatBool(copyVideo), func(t *testing.T) {
			m := manager(t, Config{})
			codec := "libx264"
			if copyVideo {
				codec = "copy"
			}
			src := Source{File: filesource.Local(source), CopyVideo: copyVideo, OutputArgs: []string{"-map", "0:v:0", "-map", "0:a:0", "-c:v", codec, "-c:a", "aac"}}
			p, err := m.Open(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if p.Duration < 20 || p.Duration > 20.1 {
				t.Fatalf("duration = %v", p.Duration)
			}
			list, err := m.Playlist(context.Background(), p.Key)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(list), "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(string(list), "#EXT-X-ENDLIST") || !strings.Contains(string(list), "seg-00003.m4s") {
				t.Fatalf("incomplete playlist: %s", list)
			}
			files, _ := os.ReadDir(m.dir)
			if len(files) != 0 {
				t.Fatal("opening a playback generated media")
			}
			// Seek near the end first. Concurrent init/media requests must share
			// one render and leave earlier fragments entirely ungenerated.
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(init bool) {
					defer wg.Done()
					f, err := m.Segment(context.Background(), p.Key, 3, init)
					if err != nil {
						t.Error(err)
						return
					}
					defer f.Close()
					data, err := io.ReadAll(f)
					if err != nil || len(data) == 0 {
						t.Errorf("empty fragment: %v", err)
					}
				}(i%2 == 0)
			}
			wg.Wait()
			files, _ = os.ReadDir(m.dir)
			if len(files) != 1 {
				t.Fatalf("generated %d fragment directories, want 1", len(files))
			}
			// A different viewer shares that cached rendition.
			other, err := m.Open(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if p.Key == other.Key {
				t.Fatal("shared capability token")
			}
			m.Release(p.Key)
			if _, err := m.Playlist(context.Background(), p.Key); !errors.Is(err, ErrNotFound) {
				t.Fatalf("released playback: %v", err)
			}
			f, err := m.Segment(context.Background(), other.Key, 3, false)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			files, _ = os.ReadDir(m.dir)
			if len(files) != 1 {
				t.Fatal("second viewer regenerated cached media")
			}
			// Every independently requested fragment must be decodable, including
			// B-frame copies, middle fragments and the short final fragment.
			for index := 0; index < 4; index++ {
				verifyFragment(t, m, other.Key, index, !copyVideo)
			}
			list2, _ := m.Playlist(context.Background(), other.Key)
			if string(list) != string(list2) {
				t.Fatal("VOD playlist changed after generation")
			}
			if _, err := m.Segment(context.Background(), other.Key, -1, false); !errors.Is(err, ErrNotFound) {
				t.Fatalf("negative index: %v", err)
			}
			if _, err := m.Segment(context.Background(), other.Key, 4, false); !errors.Is(err, ErrNotFound) {
				t.Fatalf("out-of-range index: %v", err)
			}
			if err := os.Chtimes(source, time.Now(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			// A cached old fragment remains valid, but uncached media must never
			// be generated from a changed source under its old timeline.
			m.mu.Lock()
			for id := range m.cache {
				m.remove(id)
			}
			m.mu.Unlock()
			if _, err := m.Segment(context.Background(), other.Key, 0, false); !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("changed source: %v", err)
			}
		})
	}
}

func verifyFragment(t *testing.T, m *Manager, key string, index int, transcoded bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fragment.mp4")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, init := range []bool{true, false} {
		f, err := m.Segment(context.Background(), key, index, init)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(out, f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	out.Close()
	if output, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", path, "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("decode segment %d: %v %s", index, err, output)
	}
	// Container duration can conceal an empty/truncated audio track. Verify
	// actual audio packets span the planned interval, including delayed encoders.
	audio, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var end float64
	for _, line := range strings.Split(string(audio), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pts, _ := strconv.ParseFloat(fields[0], 64)
		duration, _ := strconv.ParseFloat(fields[1], 64)
		end = max(end, pts+duration)
	}
	m.mu.Lock()
	planned := m.plays[key].stream.segments[index].duration
	m.mu.Unlock()
	if end < planned-.15 {
		t.Fatalf("segment %d audio ends at %.3f, planned %.3f", index, end, planned)
	}
	// Inspect video frame count: copying a GOP must not include B frames from
	// the following GOP or lose frames at its beginning/end.
	output, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "" {
		m.mu.Lock()
		seg := m.plays[key].stream.segments[index]
		m.mu.Unlock()
		want := seg.frames
		if transcoded {
			want = int(seg.duration*25 + .5)
		}
		frames, _ := strconv.Atoi(strings.TrimSpace(string(output)))
		if frames != want {
			t.Fatalf("segment %d frames = %d, want %d", index, frames, want)
		}
	}
}

func TestAudioVOD(t *testing.T) {
	m := manager(t, Config{})
	p, err := m.Open(context.Background(), Source{File: filesource.Local(fixture(t, true)), OutputArgs: []string{"-map", "0:a:0", "-vn", "-c:a", "aac"}})
	if err != nil {
		t.Fatal(err)
	}
	verifyFragment(t, m, p.Key, 3, false)
	verifyFragment(t, m, p.Key, 1, false)
}

func TestSharedWorkCancellation(t *testing.T) {
	m := manager(t, Config{MaxConcurrent: 1})
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	work := func(ctx context.Context) error {
		calls.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- m.do(ctx, "shared", work) }()
	<-started
	go func() { second <- m.do(context.Background(), "shared", work) }()
	deadline := time.After(5 * time.Second)
	for {
		m.mu.Lock()
		joined := m.jobs["shared"].waiters == 2
		m.mu.Unlock()
		if joined {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second waiter did not join")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(finish)
	if err := <-second; err != nil {
		t.Fatalf("one viewer canceled another: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("started %d jobs", calls.Load())
	}
	// Canceling the last waiter stops the actual job and frees its slot.
	ctx, cancel = context.WithCancel(context.Background())
	started = make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		first <- m.do(ctx, "canceled", func(ctx context.Context) error { close(started); <-ctx.Done(); close(stopped); return ctx.Err() })
	}()
	<-started
	cancel()
	<-first
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("orphaned generation job")
	}
}

func TestCapacityExpiryAndClose(t *testing.T) {
	m := manager(t, Config{MaxBytes: 10, IdleTimeout: 50 * time.Millisecond})
	// Cache accounting and LRU must evict fragments independently of active
	// playbacks, which can regenerate them on the next request.
	m.mu.Lock()
	for i := 0; i < 3; i++ {
		dir, _ := os.MkdirTemp(m.dir, "fragment-")
		id := strconv.Itoa(i)
		m.cache[id] = &cached{dir: dir, size: 5, last: time.Now().Add(time.Duration(i) * time.Millisecond)}
		m.bytes += 5
	}
	m.evict("2")
	if m.bytes != 10 || m.cache["0"] != nil {
		t.Fatal("LRU capacity eviction failed")
	}
	m.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		m.mu.Lock()
		empty := len(m.cache) == 0 && m.bytes == 0
		m.mu.Unlock()
		if empty {
			break
		}
		select {
		case <-deadline:
			t.Fatal("idle cache not cleaned")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	m.Close()
	if _, err := os.Stat(m.dir); !os.IsNotExist(err) {
		t.Fatalf("cache survives close: %v", err)
	}
	if _, err := m.Playlist(context.Background(), "unknown"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed manager: %v", err)
	}
}

// Non-integer frame rates, reordered B frames and nonzero transport timestamps
// are common in real libraries. Copying fragments must preserve every decoded
// frame exactly once, rather than merely produce files that a decoder accepts.
func TestCopyPreservesFrames(t *testing.T) {
	fixture(t, false) // require the media tools
	path := filepath.Join(t.TempDir(), "offset.ts")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=30000/1001:duration=13.1", "-c:v", "libx264", "-g", "75", "-keyint_min", "75", "-sc_threshold", "0", "-bf", "3", "-an", "-output_ts_offset", "5", "-f", "mpegts", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	m := manager(t, Config{})
	p, err := m.Open(context.Background(), Source{File: filesource.Local(path), CopyVideo: true, OutputArgs: []string{"-map", "0:v:0", "-an", "-c:v", "copy"}})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	segments := m.plays[p.Key].stream.segments
	m.mu.Unlock()
	if len(segments) != 2 {
		t.Fatalf("keyframe-aligned segments: %+v", segments)
	}
	var actual []string
	for index := range segments {
		path := filepath.Join(t.TempDir(), "fragment.mp4")
		out, _ := os.Create(path)
		for _, init := range []bool{true, false} {
			f, err := m.Segment(context.Background(), p.Key, index, init)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(out, f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		out.Close()
		actual = append(actual, frameHashes(t, path)...)
	}
	expected := frameHashes(t, path)
	if len(actual) != len(expected) {
		t.Fatalf("copied %d frames, expected %d", len(actual), len(expected))
	}
	for i := range actual {
		if actual[i] != expected[i] {
			t.Fatalf("copied frame %d differs from source", i)
		}
	}
}

func frameHashes(t *testing.T, path string) []string {
	t.Helper()
	data, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-fps_mode", "passthrough", "-f", "framemd5", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("frame hashes: %v: %s", err, data)
	}
	var hashes []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		hashes = append(hashes, strings.TrimSpace(fields[len(fields)-1]))
	}
	return hashes
}

func TestDTSRemuxAudio(t *testing.T) {
	input := fixture(t, false)
	path := filepath.Join(t.TempDir(), "dts.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", input, "-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-c:a", "dca", "-strict", "-2", path).CombinedOutput(); err != nil {
		t.Fatalf("DTS fixture: %v: %s", err, out)
	}
	m := manager(t, Config{})
	p, err := m.Open(context.Background(), Source{File: filesource.Local(path), CopyVideo: true, OutputArgs: []string{"-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-c:a", "aac"}})
	if err != nil {
		t.Fatal(err)
	}
	verifyFragment(t, m, p.Key, 2, false)
}
