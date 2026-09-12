package media

import "context"

type Query struct {
	Type     string
	Title    string
	Year     *int
	Language string
}

type Candidate struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"originalTitle,omitempty"`
	Year          *int    `json:"year,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	PosterPath    string  `json:"posterPath,omitempty"`
	BackdropPath  string  `json:"backdropPath,omitempty"`
	Popularity    float64 `json:"popularity,omitempty"`
	VoteAverage   float64 `json:"voteAverage,omitempty"`
}

type MetadataProvider interface {
	Search(context.Context, Query) ([]Candidate, error)
	Fetch(context.Context, string, string, string) (Candidate, error)
}

// EpisodeMetadataProvider is optional so other providers can still implement
// only series/movie lookup. Matchers degrade to a numbered episode when the
// provider does not expose episode-level metadata or the request fails.
type EpisodeMetadataProvider interface {
	FetchEpisode(context.Context, string, int, int, string) (Candidate, error)
}
