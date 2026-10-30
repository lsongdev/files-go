package media

import (
	"context"
	"errors"
	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/store"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProviderBreakerRecovery(t *testing.T) {
	ctx := context.Background()
	var b breaker
	for i := 0; i < 3; i++ {
		if e := b.before(ctx); e != nil {
			t.Fatal(e)
		}
		b.after(ErrTransient)
	}
	if e := b.before(ctx); !errors.Is(e, ErrTransient) {
		t.Fatal("provider did not cool down")
	}
	b.mu.Lock()
	b.until = time.Now().Add(-time.Second)
	b.mu.Unlock()
	if e := b.before(ctx); e != nil {
		t.Fatal(e)
	}
	if e := b.before(ctx); !errors.Is(e, ErrTransient) {
		t.Fatal("multiple half-open probes allowed")
	}
	b.after(nil)
	if e := b.before(ctx); e != nil {
		t.Fatal("recovered provider remained blocked")
	}
}
func TestProbeCheckpointAvoidsRepeatedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor-based subprocesses require Unix")
	}
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "track.flac"), []byte("audio"), 0600)
	trace := filepath.Join(dir, "calls")
	t.Setenv("PROBE_TRACE", trace)
	bin := filepath.Join(dir, "probe")
	script := "#!/bin/sh\nprintf x >> \"$PROBE_TRACE\"\nprintf '%s' '{\"streams\":[{\"codec_type\":\"audio\",\"codec_name\":\"flac\"}],\"format\":{\"duration\":\"60\",\"tags\":{\"album\":\"Album\"}}}'\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	st, e := store.Open(filepath.Join(dir, "files.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	scanner.Scan(ctx, "r", false)
	f, _ := st.ByPath(ctx, "r", "track.flac")
	probe := &Probe{Bin: bin, Open: scanner.Open, Store: st}
	for i := 0; i < 2; i++ {
		var m Meta
		if e := probe.Enrich(ctx, &f, &m); e != nil || m.Album != "Album" {
			t.Fatalf("probe cache: %+v %v", m, e)
		}
	}
	b, _ := os.ReadFile(trace)
	if string(b) != "x" {
		t.Fatalf("repeated probe: %q", b)
	}
	os.WriteFile(filepath.Join(root, "track.flac"), []byte("changed audio"), 0600)
	var m Meta
	if e := probe.Enrich(ctx, &f, &m); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(trace)
	if string(b) != "xx" {
		t.Fatal("changed source reused stale probe")
	}
}
