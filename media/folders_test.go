package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/tmdb-go"
)

func TestFoldersAggregateWithoutOnlineLookups(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"Movie/part1.mkv", "Movie/part2.mkv", "Movie/poster.jpg", "Mix/a.mkv", "Mix/b.mkv", "Album/CD1/a.flac", "Album/CD2/b.flac"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("x"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("directory queried catalog: %s", r.URL)
		http.Error(w, "unexpected", 500)
	}))
	defer api.Close()
	library := &Library{Store: st, Open: scanner.Open, Hint: func(store.File) (string, string) { return "auto", "" }, TMDB: newTMDB(tmdb.Options{Key: "k", BaseURL: api.URL})}
	pipeline, _ := NewPipeline(ctx, st, "folders", library.Folders())
	seed := func(name string, m Meta) {
		f, _ := st.ByPath(ctx, "r", name)
		data, _ := json.Marshal(m)
		if err := st.SaveResults(ctx, []store.Result{{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Meta: data, Version: pipeline.Version}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Movie/part1.mkv", "Movie/part2.mkv", "Mix/a.mkv"} {
		seed(name, Meta{Kind: "movie", Title: "Film", Match: &store.Match{Provider: "tmdb", Type: "movie", ID: "1"}, Poster: "self", Width: 1920, Video: "h264"})
	}
	seed("Mix/b.mkv", Meta{Kind: "movie", Title: "Other", Match: &store.Match{Provider: "tmdb", Type: "movie", ID: "2"}})
	for _, name := range []string{"Album/CD1/a.flac", "Album/CD2/b.flac"} {
		seed(name, Meta{Kind: "audio", Title: name, Album: "Album", Artist: name, AlbumArtist: "Various Artists", Duration: 60, Poster: "self", Info: map[string]string{"Track": "1"}})
	}
	for _, name := range []string{"Movie", "Mix", "Album/CD1", "Album/CD2", "Album"} {
		f, _ := st.ByPath(ctx, "r", name)
		if err := pipeline.Rematch(ctx, f); err != nil {
			t.Fatal(err)
		}
		f, _ = st.Get(ctx, f.ID)
		var m Meta
		json.Unmarshal(f.Meta, &m)
		switch name {
		case "Movie":
			poster, _ := st.ByPath(ctx, "r", "Movie/poster.jpg")
			if m.Kind != "movie" || m.Title != "Film" || m.Poster != fmt.Sprintf("file:%d", poster.ID) || m.Video != "" || m.Width != 0 {
				t.Fatalf("movie aggregate: %+v", m)
			}
		case "Mix":
			if m.Kind != "" || m.Match != nil {
				t.Fatalf("mixed folder became a movie: %+v", m)
			}
			first, _ := st.ByPath(ctx, "r", "Mix/a.mkv")
			if m.Poster != fmt.Sprintf("file:%d", first.ID) {
				t.Fatalf("mixed folder representative artwork: %+v", m)
			}
		case "Album":
			if m.Kind != "album" || m.Title != "Album" || m.Artist != "Various Artists" || m.Duration != 120 || m.Tracks != 2 || m.Info["Track"] != "" {
				t.Fatalf("album: %+v", m)
			}
		}
	}
	// Replacing artwork at the same indexed path must update the directory's
	// artwork version even though the file:<id> reference itself is unchanged.
	dir, _ := st.ByPath(ctx, "r", "Movie")
	var before Meta
	json.Unmarshal(dir.Meta, &before)
	if err := os.WriteFile(filepath.Join(root, "Movie/poster.jpg"), []byte("updated poster"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := scanner.SyncDir(ctx, dir); err != nil {
		t.Fatal(err)
	}
	dir, _ = st.Get(ctx, dir.ID)
	if err := pipeline.Rematch(ctx, dir); err != nil {
		t.Fatal(err)
	}
	dir, _ = st.Get(ctx, dir.ID)
	var after Meta
	json.Unmarshal(dir.Meta, &after)
	if after.Poster != before.Poster || after.ArtworkVersion == before.ArtworkVersion {
		t.Fatalf("artwork cache did not invalidate: before=%+v after=%+v", before, after)
	}
}

func TestAlbumAggregationIncludesEveryTrack(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Album"), 0755)
	for i := 0; i < 513; i++ {
		os.WriteFile(filepath.Join(root, "Album", fmt.Sprintf("%04d.flac", i)), []byte("x"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root}}, nil, Classify)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	dir, _ := st.ByPath(ctx, "r", "Album")
	files, err := st.MediaUnder(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Meta{Kind: "audio", Album: "Long Album", Artist: "Artist", Duration: 1})
	var results []store.Result
	for _, f := range files {
		results = append(results, store.Result{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Meta: data, Version: 1})
	}
	if err := st.SaveResults(ctx, results); err != nil {
		t.Fatal(err)
	}
	dir, _ = st.Get(ctx, dir.ID)
	var m Meta
	if err := (&Library{Store: st}).Folders().Enrich(ctx, &dir, &m); err != nil {
		t.Fatal(err)
	}
	if m.Kind != "album" || m.Tracks != 513 || m.Duration != 513 {
		t.Fatalf("truncated album: %+v", m)
	}
}
