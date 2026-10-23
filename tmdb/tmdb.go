// Package tmdb is a minimal The Movie Database client. Responses are cached
// so a season of episodes costs one search, not one per file.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable wraps network failures, rate limits and server errors.
var ErrUnavailable = errors.New("tmdb unavailable")

type Cache interface {
	CacheGet(ctx context.Context, key string, maxAge time.Duration) ([]byte, bool)
	CachePut(ctx context.Context, key string, value []byte) error
}

type Client struct {
	key, language string
	base, images  string
	http          *http.Client
	cache         Cache

	mu       sync.Mutex
	inflight map[string]*sync.Mutex // one request per cache key at a time
}

func New(key, language string, cache Cache) *Client {
	return &Client{key: strings.TrimSpace(key), language: language, cache: cache,
		base: "https://api.themoviedb.org/3", images: "https://image.tmdb.org/t/p",
		http: &http.Client{Timeout: 15 * time.Second}, inflight: map[string]*sync.Mutex{}}
}

// SetBaseURL points the client at another server, e.g. in tests.
func (c *Client) SetBaseURL(api, images string) { c.base, c.images = api, images }

type Item struct {
	ID         int      `json:"id"`
	Type       string   `json:"type"` // movie or tv
	Title      string   `json:"title"`
	Original   string   `json:"original,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	Year       int      `json:"year,omitempty"`
	Overview   string   `json:"overview,omitempty"`
	Poster     string   `json:"poster,omitempty"`
	Backdrop   string   `json:"backdrop,omitempty"`
	Rating     float64  `json:"rating,omitempty"`
	Popularity float64  `json:"popularity,omitempty"`
	Genres     []string `json:"genres,omitempty"`
	Runtime    int      `json:"runtime,omitempty"`
}

type Episode struct {
	Name     string  `json:"name"`
	Overview string  `json:"overview"`
	AirDate  string  `json:"air_date"`
	Still    string  `json:"still_path"`
	Rating   float64 `json:"vote_average"`
	Runtime  int     `json:"runtime"`
}

type raw struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	VoteAverage   float64 `json:"vote_average"`
	Popularity    float64 `json:"popularity"`
	Runtime       int     `json:"runtime"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

func (r raw) item(kind string) Item {
	it := Item{ID: r.ID, Type: kind, Title: r.Title, Original: r.OriginalTitle, Overview: r.Overview,
		Poster: r.PosterPath, Backdrop: r.BackdropPath, Rating: r.VoteAverage, Popularity: r.Popularity, Runtime: r.Runtime}
	date := r.ReleaseDate
	if kind == "tv" {
		it.Title, it.Original, date = r.Name, r.OriginalName, r.FirstAirDate
	}
	if len(date) >= 4 {
		it.Year, _ = strconv.Atoi(date[:4])
	}
	for _, g := range r.Genres {
		it.Genres = append(it.Genres, g.Name)
	}
	return it
}

// Search finds movies or tv shows. Results use the configured language;
// English titles are merged in as aliases so English filenames still match.
func (c *Client) Search(ctx context.Context, kind, title string, year int) ([]Item, error) {
	if kind != "movie" && kind != "tv" {
		return nil, fmt.Errorf("invalid tmdb type %q", kind)
	}
	items, err := c.search(ctx, kind, title, year, c.language)
	if err != nil || strings.HasPrefix(strings.ToLower(c.language), "en") {
		return items, err
	}
	english, err := c.search(ctx, kind, title, year, "en-US")
	if err != nil {
		return items, nil
	}
	index := map[int]int{}
	for i, it := range items {
		index[it.ID] = i
	}
	for _, it := range english {
		if i, ok := index[it.ID]; ok {
			items[i].Aliases = append(items[i].Aliases, it.Title)
		} else {
			it.Aliases = []string{it.Title}
			items = append(items, it)
		}
	}
	return items, nil
}

func (c *Client) search(ctx context.Context, kind, title string, year int, language string) ([]Item, error) {
	query := url.Values{"query": {title}, "language": {language}, "include_adult": {"false"}}
	if year > 0 {
		if kind == "movie" {
			query.Set("year", strconv.Itoa(year))
		} else {
			query.Set("first_air_date_year", strconv.Itoa(year))
		}
	}
	var out struct{ Results []raw }
	if err := c.get(ctx, "/search/"+kind, query, &out); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(out.Results))
	for _, r := range out.Results {
		items = append(items, r.item(kind))
	}
	return items, nil
}

func (c *Client) Details(ctx context.Context, kind string, id int) (Item, error) {
	var out raw
	if err := c.get(ctx, fmt.Sprintf("/%s/%d", kind, id), url.Values{"language": {c.language}}, &out); err != nil {
		return Item{}, err
	}
	return out.item(kind), nil
}

func (c *Client) Episode(ctx context.Context, show, season, episode int) (Episode, error) {
	var out Episode
	err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", show, season, episode), url.Values{"language": {c.language}}, &out)
	return out, err
}

var errNotFound = errors.New("tmdb: not found")

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, out any) error {
	key := "tmdb:" + endpoint + "?" + query.Encode()
	// Workers enriching episodes of one show ask for the same search at
	// once; serialize per key so only the first one hits the network.
	c.mu.Lock()
	lock, ok := c.inflight[key]
	if !ok {
		lock = &sync.Mutex{}
		c.inflight[key] = lock
	}
	c.mu.Unlock()
	lock.Lock()
	defer func() {
		lock.Unlock()
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
	}()
	if data, fresh := c.cache.CacheGet(ctx, key, 30*24*time.Hour); fresh {
		if string(data) == "null" {
			return errNotFound
		}
		return json.Unmarshal(data, out)
	}
	bearer := strings.HasPrefix(c.key, "eyJ") // v4 read access token
	if !bearer {
		query.Set("api_key", c.key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case err != nil:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	case res.StatusCode == http.StatusNotFound:
		_ = c.cache.CachePut(ctx, key, []byte("null"))
		return errNotFound
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("tmdb: HTTP %d", res.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", endpoint, err)
	}
	_ = c.cache.CachePut(ctx, key, data)
	return nil
}

// Image downloads artwork, e.g. Image(ctx, "w500", "/abc.jpg").
func (c *Client) Image(ctx context.Context, size, path string) (io.ReadCloser, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid image path %q", path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.images+"/"+size+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("tmdb image: HTTP %d", res.StatusCode)
	}
	return res.Body, nil
}

// IsNotFound reports a 404 from TMDB, e.g. an unknown episode.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }
