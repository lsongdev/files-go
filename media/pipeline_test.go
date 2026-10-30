package media

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/store"
)

type tagFixture struct{}

func (tagFixture) Match(f *store.File) bool { return f.Kind == "audio" }
func (tagFixture) Enrich(_ context.Context, f *store.File, m *Meta) error {
	m.Kind, m.Title, m.Artist, m.AlbumArtist, m.Album = "audio", f.Name, f.Name, "Various Artists", "Album"
	m.Duration, m.Poster = 60, "self"
	return nil
}

func TestAggregationConvergesAfterSourceChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"Album/CD1/a.flac", "Album/CD2/b.flac"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("audio"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	scan := func() {
		t.Helper()
		if err := scanner.Scan(ctx, "r", false); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	library := &Library{Store: st}
	pipeline, _ := NewPipeline(ctx, st, "changes", tagFixture{}, library.Folders())
	read := func() Meta { f, _ := st.ByPath(ctx, "r", "Album"); var m Meta; json.Unmarshal(f.Meta, &m); return m }
	runPipeline(t, ctx, st, pipeline)
	if m := read(); m.Kind != "album" || m.Tracks != 2 || m.Duration != 120 {
		t.Fatalf("initial: %+v", m)
	}
	os.Remove(filepath.Join(root, "Album/CD1/a.flac"))
	scan()
	runPipeline(t, ctx, st, pipeline)
	if m := read(); m.Kind != "album" || m.Tracks != 1 || m.Duration != 60 {
		t.Fatalf("after removal: %+v", m)
	}
	os.WriteFile(filepath.Join(root, "Album/cover.jpg"), []byte("poster"), 0600)
	scan()
	runPipeline(t, ctx, st, pipeline)
	cover, _ := st.ByPath(ctx, "r", "Album/cover.jpg")
	if m := read(); m.Poster != "file:"+strconv.FormatInt(cover.ID, 10) {
		t.Fatalf("local artwork: %+v", m)
	}
	os.Remove(filepath.Join(root, "Album/CD2/b.flac"))
	scan()
	runPipeline(t, ctx, st, pipeline)
	if m := read(); m.Kind != "" || m.Tracks != 0 || m.Duration != 0 || m.Match != nil {
		t.Fatalf("empty aggregate kept album: %+v", m)
	}
}

type blockedFixture struct{ started, release chan struct{} }

func (*blockedFixture) Match(f *store.File) bool { return !f.Dir }
func (b *blockedFixture) Enrich(ctx context.Context, _ *store.File, m *Meta) error {
	close(b.started)
	select {
	case <-b.release:
		m.Title = "old"
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRunningWorkerCannotOverwriteManualChoice(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.mkv"), []byte("video"), 0600)
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	scanner.Scan(ctx, "r", false)
	f, _ := st.ByPath(ctx, "r", "a.mkv")
	b := &blockedFixture{make(chan struct{}), make(chan struct{})}
	p, _ := NewPipeline(ctx, st, "blocked", b)
	done := make(chan error, 1)
	go func() { done <- p.Rematch(ctx, f) }()
	<-b.started
	choice := &store.Match{Provider: "tmdb", Type: "movie", ID: "123"}
	if err := st.SetMatch(ctx, f, choice); err != nil {
		t.Fatal(err)
	}
	close(b.release)
	if err := <-done; !errors.Is(err, store.ErrStale) {
		t.Fatalf("worker save: %v", err)
	}
	f, _ = st.Get(ctx, f.ID)
	if f.MetaVer != 0 || len(f.Meta) > 0 {
		t.Fatalf("old result was saved: %+v", f)
	}
	if got, err := st.Match(ctx, f); err != nil || got == nil || *got != *choice {
		t.Fatalf("manual choice lost: %+v %v", got, err)
	}
}

type failingFixture struct{}

func (failingFixture) Match(f *store.File) bool { return !f.Dir }
func (failingFixture) Enrich(_ context.Context, f *store.File, m *Meta) error {
	if f.Name == "a.mkv" {
		return ErrTransient
	}
	m.Title = "ready"
	return nil
}

func TestTransientFailureDoesNotPauseOtherFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	for _, name := range []string{"a.mkv", "b.flac"} {
		os.WriteFile(filepath.Join(root, name), []byte("x"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	scanner.Scan(ctx, "r", false)
	p, _ := NewPipeline(ctx, st, "retry", failingFixture{})
	done := make(chan struct{})
	go func() { p.Run(ctx, 1); close(done) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(4 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		f, _ := st.ByPath(ctx, "r", "b.flac")
		if f.MetaVer == p.Version {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unrelated file paused after transient failure")
		}
	}
	f, _ := st.ByPath(ctx, "r", "a.mkv")
	if f.MetaVer != 0 {
		t.Fatal("failed task was marked complete")
	}
}
