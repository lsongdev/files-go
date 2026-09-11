package media

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
	"time"
)

type TMDB struct {
	token, baseURL string
	client         *http.Client
}

func NewTMDB(token string, client *http.Client) *TMDB {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &TMDB{token: strings.TrimSpace(token), baseURL: "https://api.themoviedb.org/3", client: client}
}

func (p *TMDB) Search(ctx context.Context, query Query) ([]Candidate, error) {
	if query.Type != "movie" && query.Type != "tv" {
		return nil, errors.New("TMDB search type must be movie or tv")
	}
	values := url.Values{"query": {query.Title}, "include_adult": {"false"}}
	if query.Language != "" {
		values.Set("language", query.Language)
	}
	if query.Year != nil {
		if query.Type == "movie" {
			values.Set("year", strconv.Itoa(*query.Year))
		} else {
			values.Set("first_air_date_year", strconv.Itoa(*query.Year))
		}
	}
	var response struct {
		Results []tmdbItem `json:"results"`
	}
	if err := p.get(ctx, "/search/"+query.Type, values, &response); err != nil {
		return nil, err
	}
	items := make([]Candidate, 0, len(response.Results))
	for _, item := range response.Results {
		items = append(items, item.candidate(query.Type))
	}
	return items, nil
}

func (p *TMDB) Fetch(ctx context.Context, itemType, id, language string) (Candidate, error) {
	if itemType != "movie" && itemType != "tv" {
		return Candidate{}, errors.New("TMDB item type must be movie or tv")
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return Candidate{}, errors.New("invalid TMDB ID")
	}
	values := url.Values{}
	if language != "" {
		values.Set("language", language)
	}
	var item tmdbItem
	if err := p.get(ctx, "/"+itemType+"/"+id, values, &item); err != nil {
		return Candidate{}, err
	}
	return item.candidate(itemType), nil
}

func (p *TMDB) get(ctx context.Context, endpoint string, values url.Values, output any) error {
	if p.token == "" {
		return errors.New("TMDB token is not configured")
	}
	requestURL := p.baseURL + endpoint
	if !strings.HasPrefix(p.token, "eyJ") {
		values.Set("api_key", p.token)
	}
	if encoded := values.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	if strings.HasPrefix(p.token, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return fmt.Errorf("TMDB returned HTTP %d", res.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 4<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode TMDB response: %w", err)
	}
	return nil
}

type tmdbItem struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Popularity    float64 `json:"popularity"`
	VoteAverage   float64 `json:"vote_average"`
}

func (i tmdbItem) candidate(itemType string) Candidate {
	title, original, date := i.Title, i.OriginalTitle, i.ReleaseDate
	if itemType == "tv" {
		title, original, date = i.Name, i.OriginalName, i.FirstAirDate
	}
	item := Candidate{ID: strconv.FormatInt(i.ID, 10), Type: itemType, Title: title, OriginalTitle: original, Overview: i.Overview, PosterPath: i.PosterPath, BackdropPath: i.BackdropPath, Popularity: i.Popularity, VoteAverage: i.VoteAverage}
	if len(date) >= 4 {
		if year, err := strconv.Atoi(date[:4]); err == nil {
			item.Year = &year
		}
	}
	return item
}
