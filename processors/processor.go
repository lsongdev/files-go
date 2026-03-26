package processors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lsongdev/files-go/types"
)

// TMDB API response structures
type TMDBSearchResult struct {
	Results []TMDBMovie `json:"results"`
}

type TMDBMovie struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
}

type FileProcessor interface {
	IsSupport(file *types.File) bool
	Process(file *types.File) error
}

var tmdbAPIKey string

func SetTMDBAPIKey(key string) {
	tmdbAPIKey = key
}

func GetProcessors() []FileProcessor {
	return []FileProcessor{
		&VideoProcessor{},
		&EpubProcessor{},
		&MusicProcessor{},
		&ImageProcessor{},
		&APKProcessor{},
		&DefaultProcessor{},
	}
}

func searchTMDB(query string) (*TMDBMovie, error) {
	if tmdbAPIKey == "" {
		return nil, fmt.Errorf("TMDB API key not configured")
	}

	url := fmt.Sprintf("https://api.themoviedb.org/3/search/multi?api_key=%s&query=%s&language=zh-CN&page=1",
		tmdbAPIKey, query)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result TMDBSearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if len(result.Results) > 0 {
		return &result.Results[0], nil
	}

	return nil, nil
}

func getReleaseDate(movie *TMDBMovie) string {
	if movie.ReleaseDate != "" {
		return movie.ReleaseDate
	}
	if movie.FirstAirDate != "" {
		return movie.FirstAirDate
	}
	return ""
}
