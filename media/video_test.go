package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/tmdb-go"
)

// fakeVideo stands in for ffprobe.
type fakeVideo struct{}

func (fakeVideo) Match(f *store.File) bool { return f.Kind == "video" }
func (fakeVideo) Enrich(_ context.Context, _ *store.File, m *Meta) error {
	m.Kind, m.Video = "video", "h264"
	return nil
}

func TestIdentifyMoviesAndEpisodes(t *testing.T) {
	ctx := context.Background()
	var searches atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/tv":
			searches.Add(1)
			w.Write([]byte(`{"results":[{"id":100,"name":"最后生还者","original_name":"The Last of Us","first_air_date":"2023-01-15","poster_path":"/tlou.jpg"}]}`))
		case "/tv/100":
			w.Write([]byte(`{"id":100,"name":"最后生还者","original_name":"The Last of Us","first_air_date":"2023-01-15","poster_path":"/tlou.jpg","genres":[{"name":"剧情"}]}`))
		case "/tv/100/season/1":
			w.Write([]byte(`{"season_number":1,"episodes":[{"episode_number":3,"name":"漫长岁月","air_date":"2023-01-29","still_path":"/still.jpg"},{"episode_number":4,"name":"请抓紧我的手"}]}`))
		case "/search/movie":
			searches.Add(1)
			w.Write([]byte(`{"results":[{"id":7,"title":"Dune (1984)","release_date":"1984-12-14"},{"id":200,"title":"沙丘","original_title":"Dune","release_date":"2021-09-15"}]}`))
		case "/movie/200":
			w.Write([]byte(`{"id":200,"title":"沙丘","original_title":"Dune","release_date":"2021-09-15","overview":"spice","runtime":155,"backdrop_path":"/bd.jpg","poster_path":"/dune.jpg"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, name := range []string{
		"TV/The Last of Us (2023)/Season 1/The.Last.of.Us.S01E03.1080p.mkv",
		"TV/The Last of Us (2023)/Season 1/E04.mkv",
		"Movies/Dune (2021)/Dune.2021.2160p.WEB-DL.mkv",
		"Movies/Dune (2021)/poster.jpg",
		"Clips/holiday.mp4",
	} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644)
	}
	st, err := store.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfgRoot := config.Root{ID: "r", Name: "Videos", Path: root, Types: map[string]string{"TV": "tv", "Movies": "movies"}}
	scanner := store.NewScanner(st, []config.Root{cfgRoot}, nil, func(name string, size int64) string {
		if Ext(name) == "jpg" {
			return "image"
		}
		return "video"
	})
	if err := scanner.Scan(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	client := newTMDB(tmdb.Options{Key: "key", Language: "zh-CN", BaseURL: api.URL, Cache: storeCache{st}})
	library := &Library{Store: st, TMDB: client, Path: scanner.Abs, Hint: func(f store.File) (string, string) { return cfgRoot.TypeOf(f.Path) }}
	pipeline, _ := NewPipeline(ctx, st, "test", fakeVideo{}, library.Videos(), library.Folders())
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { pipeline.Run(run, 3); close(done) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if stats, _ := st.Stats(ctx, pipeline.Version); stats.Pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("enrichment did not finish")
		}
	}
	stop()
	<-done
	get := func(path string) Meta {
		f, err := st.ByPath(ctx, "r", path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var m Meta
		json.Unmarshal(f.Meta, &m)
		return m
	}

	ep := get("TV/The Last of Us (2023)/Season 1/The.Last.of.Us.S01E03.1080p.mkv")
	if ep.Kind != "episode" || ep.Show != "最后生还者" || ep.Season != 1 || ep.Episode != 3 || ep.Title != "漫长岁月" || ep.Poster != "tmdb:/still.jpg" {
		t.Errorf("episode = %+v", ep)
	}
	if ep := get("TV/The Last of Us (2023)/Season 1/E04.mkv"); ep.Kind != "episode" || ep.Episode != 4 || ep.TMDB != "tv/100" {
		t.Errorf("episode-only name = %+v", ep)
	}
	if show := get("TV/The Last of Us (2023)"); show.Kind != "show" || show.TMDB != "tv/100" || show.Year != 2023 {
		t.Errorf("show folder = %+v", show)
	}
	if season := get("TV/The Last of Us (2023)/Season 1"); season.Kind != "season" || season.Season != 1 {
		t.Errorf("season folder = %+v", season)
	}
	movie := get("Movies/Dune (2021)/Dune.2021.2160p.WEB-DL.mkv")
	if movie.Kind != "movie" || movie.Title != "沙丘" || movie.Original != "Dune" || movie.TMDB != "movie/200" || movie.Info["Release"] != "2160p · WEB-DL" {
		t.Errorf("movie = %+v", movie)
	}
	if folder := get("Movies/Dune (2021)"); folder.Kind != "movie" || folder.TMDB != "movie/200" || folder.Poster[:5] != "file:" || folder.Backdrop != "tmdb:/bd.jpg" {
		t.Errorf("movie folder (local poster wins) = %+v", folder)
	}
	if clip := get("Clips/holiday.mp4"); clip.Kind != "video" || clip.TMDB != "" {
		t.Errorf("plain clip = %+v", clip)
	}
	// One search per title and language, however many files share it.
	if n := searches.Load(); n != 4 {
		t.Errorf("TMDB searches = %d, want 4 (tv+movie × zh+en)", n)
	}

	// A manual match survives re-enrichment and overrides the guess.
	f, _ := st.ByPath(ctx, "r", "Clips/holiday.mp4")
	f.Meta, _ = json.Marshal(Meta{Locked: true, TMDB: "movie/200"})
	if err := pipeline.Rematch(ctx, f); err != nil {
		t.Fatal(err)
	}
	if clip := get("Clips/holiday.mp4"); clip.Kind != "movie" || clip.Title != "沙丘" || !clip.Locked {
		t.Errorf("manually matched clip = %+v", clip)
	}
}
