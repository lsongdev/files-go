package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lsongdev/tmdb-go"

	"github.com/lsongdev/files-go/store"
)

// TMDB adapts the tmdb-go client to identification: results become Items,
// English titles are merged in as aliases so English filenames match a
// localized catalog, and responses are cached in the index database.
type TMDB struct {
	client  *tmdb.Client
	english *tmdb.Client
	gate    breaker
	http    *http.Client
}

// Item is a movie or show as identification and the match dialog use it.
type Item struct {
	ID         int      `json:"id"`
	Type       string   `json:"type"` // movie or tv
	Title      string   `json:"title"`
	Original   string   `json:"original,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`    // English title in a localized catalog
	Alternates []string `json:"alternates,omitempty"` // alternative titles, from details
	Year       int      `json:"year,omitempty"`
	Overview   string   `json:"overview,omitempty"`
	Poster     string   `json:"poster,omitempty"`
	Backdrop   string   `json:"backdrop,omitempty"`
	Rating     float64  `json:"rating,omitempty"`
	Popularity float64  `json:"popularity,omitempty"`
	Genres     []string `json:"genres,omitempty"`
	Runtime    int      `json:"runtime,omitempty"`  // minutes, movies
	Seasons    int      `json:"seasons,omitempty"`  // shows
	Episodes   int      `json:"episodes,omitempty"` // shows
	// lead marks one of TMDB's own top results in either language. TMDB also
	// searches alternative titles that results do not carry, so these are
	// worth verifying against details even when no listed title matches.
	lead bool
}

const tmdbCacheAge = 30 * 24 * time.Hour

func NewTMDB(key, language string, st *store.Store) *TMDB {
	_ = st.CachePrune(context.Background(), "tmdb:", tmdbCacheAge)
	return newTMDB(tmdb.Options{Key: key, Language: language, Cache: storeCache{st}})
}

func newTMDB(opts tmdb.Options) *TMDB {
	client := tmdb.New(opts)
	return &TMDB{client: client, english: client.WithLanguage("en-US"), http: &http.Client{Timeout: 30 * time.Second}}
}

// storeCache keeps TMDB responses in the index's cache table for 30 days.
type storeCache struct{ st *store.Store }

func (c storeCache) Get(ctx context.Context, key string) ([]byte, bool) {
	data, fresh := c.st.CacheGet(ctx, "tmdb:"+key, tmdbCacheAge)
	return data, fresh
}

func (c storeCache) Set(ctx context.Context, key string, value []byte) {
	_ = c.st.CachePut(ctx, "tmdb:"+key, value)
}

// searchLeads is how many of TMDB's own top results per language are verified.
const searchLeads = 2

// Search finds movies ("movie") or shows ("tv").
func (t *TMDB) searchLocalized(ctx context.Context, kind, title string, year int) ([]Item, error) {
	items, err := t.search(ctx, t.client, kind, title, year)
	for i := range items {
		items[i].lead = i < searchLeads
	}
	if err != nil || strings.HasPrefix(strings.ToLower(t.client.Language()), "en") || t.client.Language() == "" {
		return items, err
	}
	english, err := t.search(ctx, t.english, kind, title, year)
	if err != nil {
		return items, nil // aliases are a bonus
	}
	index := map[int]int{}
	for i, it := range items {
		index[it.ID] = i
	}
	for n, it := range english {
		if i, ok := index[it.ID]; ok {
			items[i].Aliases = append(items[i].Aliases, it.Title)
			items[i].lead = items[i].lead || n < searchLeads
		} else {
			it.Aliases, it.lead = []string{it.Title}, n < searchLeads
			items = append(items, it)
		}
	}
	return items, nil
}

func (t *TMDB) search(ctx context.Context, c *tmdb.Client, kind, title string, year int) ([]Item, error) {
	opts := &tmdb.SearchOptions{Year: year, IncludeAdult: kind == "movie"}
	var items []Item
	switch kind {
	case "movie":
		page, err := c.SearchMovies(ctx, title, opts)
		if err != nil {
			return nil, err
		}
		for _, m := range page.Results {
			items = append(items, movieItem(m))
		}
	case "tv":
		page, err := c.SearchTV(ctx, title, opts)
		if err != nil {
			return nil, err
		}
		for _, s := range page.Results {
			items = append(items, showItem(s))
		}
	default:
		return nil, fmt.Errorf("invalid tmdb type %q", kind)
	}
	return items, nil
}

// Details fetches a movie or show with genres and runtime.
func (t *TMDB) details(ctx context.Context, kind string, id int) (Item, error) {
	if kind == "tv" {
		s, err := t.client.TV(ctx, id)
		if err != nil {
			return Item{}, err
		}
		return showItem(*s), nil
	}
	m, err := t.client.Movie(ctx, id)
	if err != nil {
		return Item{}, err
	}
	return movieItem(*m), nil
}

// Episode looks an episode up in its season, so a whole season costs one
// (cached) request.
func (t *TMDB) episode(ctx context.Context, show, season, episode int) (*tmdb.Episode, error) {
	s, err := t.client.Season(ctx, show, season)
	if err != nil {
		return nil, err
	}
	for i := range s.Episodes {
		if s.Episodes[i].EpisodeNumber == episode {
			return &s.Episodes[i], nil
		}
	}
	return nil, &tmdb.Error{Status: http.StatusNotFound}
}

// Image downloads artwork, e.g. Image(ctx, "w500", "/abc.jpg").
func (t *TMDB) Image(ctx context.Context, size, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.client.ImageURL(path, size), nil)
	if err != nil {
		return nil, err
	}
	res, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, &tmdb.Error{Status: res.StatusCode}
	}
	return res.Body, nil
}

func movieItem(m tmdb.Movie) Item {
	return Item{ID: m.ID, Type: "movie", Title: m.Title, Original: m.OriginalTitle, Year: m.Year(), Overview: m.Overview,
		Poster: m.PosterPath, Backdrop: m.BackdropPath, Rating: m.VoteAverage, Popularity: m.Popularity,
		Genres: genres(m.Genres), Runtime: m.Runtime, Alternates: alternatives(m.AlternativeTitles)}
}

func showItem(s tmdb.TV) Item {
	return Item{ID: s.ID, Type: "tv", Title: s.Name, Original: s.OriginalName, Year: s.Year(), Overview: s.Overview,
		Poster: s.PosterPath, Backdrop: s.BackdropPath, Rating: s.VoteAverage, Popularity: s.Popularity, Genres: genres(s.Genres),
		Seasons: s.NumberOfSeasons, Episodes: s.NumberOfEpisodes, Alternates: alternatives(s.AlternativeTitles)}
}

// alternatives lists distinct alternative titles, e.g. romanizations and
// former English titles that release names often use.
func alternatives(a *tmdb.AlternativeTitles) []string {
	var titles []string
	seen := map[string]bool{}
	for _, t := range a.All() {
		if t.Title != "" && !seen[t.Title] {
			seen[t.Title] = true
			titles = append(titles, t.Title)
		}
	}
	return titles
}

func genres(list []tmdb.Genre) []string {
	var names []string
	for _, g := range list {
		names = append(names, g.Name)
	}
	return names
}

func (t *TMDB) Search(ctx context.Context, kind, title string, year int) (items []Item, err error) {
	if err = t.gate.before(ctx); err != nil {
		return
	}
	defer func() { t.gate.after(transient(err)) }()
	return t.searchLocalized(ctx, kind, title, year)
}
func (t *TMDB) Details(ctx context.Context, kind string, id int) (item Item, err error) {
	if err = t.gate.before(ctx); err != nil {
		return
	}
	defer func() { t.gate.after(transient(err)) }()
	return t.details(ctx, kind, id)
}
func (t *TMDB) Episode(ctx context.Context, show, season, episode int) (item *tmdb.Episode, err error) {
	if err = t.gate.before(ctx); err != nil {
		return
	}
	defer func() { t.gate.after(transient(err)) }()
	return t.episode(ctx, show, season, episode)
}
