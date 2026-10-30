package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
			if m.Poster != "" {
				t.Fatalf("a collection of different works borrowed a member's cover: %+v", m)
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

// Layouts from a real TV library: specials without episode numbers, a
// soundtrack folder, per-episode release folders with promo clips, and season
// names that are not plain "Season N".
func TestShowFoldersTolerateExtras(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	videos := []string{
		"TV/Show/S01/Show.S01E01.mkv", "TV/Show/S01/Show.S01E02.mkv", "TV/Show/S01/Show.S01.Prequel.mp4",
		"TV/Show/S02/Show.S02E01.1080p/Show.S02E01.1080p.mkv", "TV/Show/S02/Show.S02E01.1080p/RARBG.mp4",
		"TV/Show/S02/Show.S02E02.1080p/Show.S02E02.1080p.mkv",
		"TV/Show/生活大爆炸 第十季BT全/Show.S10E01.mp4",
		"TV/Show/Show.The.Movie.2009.mkv",
		"TV/Show/Mixed/Show.S03E01.mkv", "TV/Show/Mixed/Other.S01E01.mkv",
		"Downloads/Show.S01E05.mkv", "Downloads/holiday.mp4", "Downloads/party.mp4",
	}
	for _, name := range append(videos, "TV/Show/mp3/theme.mp3") {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("x"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfgRoot := config.Root{ID: "r", Path: root, Types: map[string]string{"TV": "tv"}}
	scanner := store.NewScanner(st, []config.Root{cfgRoot}, nil, Classify)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	library := &Library{Store: st, Open: scanner.Open, Hint: func(f store.File) (string, string) { return cfgRoot.TypeOf(f.Path) }}
	pipeline, _ := NewPipeline(ctx, st, "shows", library.Folders())
	show := &Series{Title: "Show", Year: 2015, Poster: "tmdb:/show.jpg", Review: "ambiguous"}
	other := &Series{Title: "Other"}
	for _, name := range videos {
		f, _ := st.ByPath(ctx, "r", name)
		n := ParseName(f.Name)
		m := Meta{Kind: "video"}
		if n.Episode > 0 {
			m = Meta{Kind: "episode", Season: n.Season, Episode: n.Episode, Title: fmt.Sprintf("Episode %d", n.Episode), Series: show,
				Match: &store.Match{Provider: "tmdb", Type: "tv", ID: "1"}}
			if strings.HasPrefix(f.Name, "Other") {
				m.Series, m.Match = other, &store.Match{Provider: "tmdb", Type: "tv", ID: "2"}
			}
		}
		data, _ := json.Marshal(m)
		if err := st.SaveResults(ctx, []store.Result{{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Meta: data, Version: pipeline.Version}}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(name string) Meta {
		f, _ := st.ByPath(ctx, "r", name)
		if err := pipeline.Rematch(ctx, f); err != nil {
			t.Fatal(err)
		}
		f, _ = st.Get(ctx, f.ID)
		var m Meta
		json.Unmarshal(f.Meta, &m)
		return m
	}
	if m := get("TV/Show/S01"); m.Kind != "season" || m.Season != 1 || m.Title != "Show" || m.Review != "" {
		t.Errorf("season with an unnumbered special: %+v", m)
	}
	if m := get("TV/Show/S02/Show.S02E01.1080p"); m.Kind != "episode" || m.Season != 2 || m.Episode != 1 || m.Title != "Episode 1" || m.Review != "" {
		t.Errorf("episode release folder with a promo clip: %+v", m)
	}
	get("TV/Show/S02/Show.S02E02.1080p")
	if m := get("TV/Show/S02"); m.Kind != "season" || m.Season != 2 || m.Files != 3 {
		t.Errorf("season of episode folders: %+v", m)
	}
	if m := get("TV/Show/生活大爆炸 第十季BT全"); m.Kind != "season" || m.Season != 10 {
		t.Errorf("Chinese season name: %+v", m)
	}
	if m := get("TV/Show/Mixed"); m.Kind != "" || m.Match != nil {
		t.Errorf("two different shows are not one: %+v", m)
	}
	get("TV/Show/mp3")
	if m := get("TV/Show"); m.Kind != "show" || m.Title != "Show" || m.Match == nil || m.Match.ID != "1" || m.Poster != "tmdb:/show.jpg" || m.Review != "ambiguous" {
		t.Errorf("show with specials, a movie, a soundtrack and a mixed folder: %+v", m)
	}
	if m := get("Downloads"); m.Kind != "" {
		t.Errorf("one episode among other videos outside a TV library: %+v", m)
	}
}

// A guess awaiting review stays on the file; the folder follows once the
// identity is settled, and a choice for one part covers the whole movie.
func TestUnsettledMoviesStayOnTheFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"Film/Film.CD1.mkv", "Film/Film.CD2.mkv", "Single/Single.2016.mkv"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("x"), 0600)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root, Type: "movies"}}, nil, Classify)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	library := &Library{Store: st, Open: scanner.Open, Hint: func(store.File) (string, string) { return "movies", "" }}
	pipeline, _ := NewPipeline(ctx, st, "unsettled", library.Folders())
	seed := func(name string, m Meta) {
		f, _ := st.ByPath(ctx, "r", name)
		data, _ := json.Marshal(m)
		if err := st.SaveResults(ctx, []store.Result{{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Meta: data, Version: pipeline.Version}}); err != nil {
			t.Fatal(err)
		}
	}
	guess := Meta{Kind: "movie", Title: "Film", Match: &store.Match{Provider: "tmdb", Type: "movie", ID: "1"}, Review: "ambiguous"}
	seed("Film/Film.CD1.mkv", guess)
	seed("Film/Film.CD2.mkv", guess)
	single := guess
	single.Title, single.Match = "Single", &store.Match{Provider: "tmdb", Type: "movie", ID: "2"}
	seed("Single/Single.2016.mkv", single)
	folder := func(name string) (store.File, Meta) {
		f, _ := st.ByPath(ctx, "r", name)
		if err := pipeline.Rematch(ctx, f); err != nil {
			t.Fatal(err)
		}
		f, _ = st.Get(ctx, f.ID)
		var m Meta
		json.Unmarshal(f.Meta, &m)
		return f, m
	}
	for _, name := range []string{"Film", "Single"} {
		if _, m := folder(name); m.Kind != "" || m.Match != nil || m.Review != "" {
			t.Errorf("%s repeats a guess awaiting review: %+v", name, m)
		}
	}
	dir, _ := folder("Film")
	part, _ := st.ByPath(ctx, "r", "Film/Film.CD2.mkv")
	if scope, err := pipeline.MatchScope(ctx, part); err != nil || scope.ID != dir.ID {
		t.Errorf("a choice for one part should cover the movie: %+v, %v", scope, err)
	}
	if ok, err := pipeline.SharedWork(ctx, dir); !ok || err != nil {
		t.Errorf("parts of one movie are a shared work: %v, %v", ok, err)
	}
	file, _ := st.ByPath(ctx, "r", "Single/Single.2016.mkv")
	if scope, _ := pipeline.MatchScope(ctx, file); scope.ID != file.ID {
		t.Errorf("a single file keeps its own choice: %+v", scope)
	}
	settledMeta := single
	settledMeta.Review, settledMeta.Locked = "", true
	seed("Single/Single.2016.mkv", settledMeta)
	if _, m := folder("Single"); m.Kind != "movie" || m.Title != "Single" || !m.Locked {
		t.Errorf("a settled movie names its folder: %+v", m)
	}
}
