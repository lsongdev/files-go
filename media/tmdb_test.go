package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTMDBSearchAndFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer eyJ-test" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/3/search/movie":
			if r.URL.Query().Get("query") != "Interstellar" || r.URL.Query().Get("year") != "2014" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"results":[{"id":157336,"title":"Interstellar","release_date":"2014-11-05","poster_path":"/poster.jpg"}]}`))
		case "/3/movie/157336":
			_, _ = w.Write([]byte(`{"id":157336,"title":"Interstellar","release_date":"2014-11-05","overview":"Space"}`))
		case "/3/tv/100/season/1/episode/3":
			if r.URL.Query().Get("language") != "en-US" {
				t.Errorf("episode query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"id":300,"name":"Long, Long Time","air_date":"2023-01-29","overview":"Bill and Frank","still_path":"/still.jpg","vote_average":8.8}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := NewTMDB("eyJ-test", server.Client())
	provider.baseURL = server.URL + "/3"
	year := 2014
	items, err := provider.Search(context.Background(), Query{Type: "movie", Title: "Interstellar", Year: &year})
	if err != nil || len(items) != 1 || items[0].ID != "157336" || items[0].Year == nil || *items[0].Year != year {
		t.Fatalf("search = %#v, %v", items, err)
	}
	details, err := provider.Fetch(context.Background(), "movie", "157336", "en-US")
	if err != nil || details.Overview != "Space" {
		t.Fatalf("details = %#v, %v", details, err)
	}
	episode, err := provider.FetchEpisode(context.Background(), "100", 1, 3, "en-US")
	if err != nil || episode.Title != "Long, Long Time" || episode.Year == nil || *episode.Year != 2023 || episode.PosterPath != "/still.jpg" {
		t.Fatalf("episode = %#v, %v", episode, err)
	}
}
