package theporndb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestSearch(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/scenes" || r.URL.Query().Get("q") != "a & b" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		w.Write([]byte(`{"data":[{"id":"uuid","duration":null,"site":{"id":1},"links":[]}],"meta":{"current_page":2,"last_page":3,"total":6,"per_page":2}}`))
	}))
	defer s.Close()
	c, err := New(Options{Token: "test", BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.SearchScenes(context.Background(), url.Values{"q": {"a & b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Data) != 1 || p.Data[0].ID != "uuid" || p.Data[0].Duration != nil || p.Meta.LastPage != 3 {
		t.Fatalf("unexpected page: %+v", p)
	}
}

func TestErrors(t *testing.T) {
	for _, body := range []string{`{"message":"rate limited"}`, `<html>unavailable</html>`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
			w.Write([]byte(body))
		}))
		c, _ := New(Options{Token: "test", BaseURL: s.URL})
		_, err := c.GetScene(context.Background(), "id", nil)
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 || apiErr.RetryAfter != "60" || apiErr.Body != body {
			t.Fatalf("unexpected error: %v", err)
		}
		s.Close()
	}
}

func TestValidationAndCancellation(t *testing.T) {
	for _, o := range []Options{{}, {Token: "test", BaseURL: ":bad"}, {Token: "test", BaseURL: "https://example.com?key=secret"}} {
		if _, err := New(o); err == nil {
			t.Fatal("expected validation error")
		}
	}
	c, _ := New(Options{Token: "test"})
	if _, err := c.GetScene(context.Background(), "..", nil); err == nil {
		t.Fatal("expected invalid identifier")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.SearchScenes(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}

// TestLive is opt-in and never stores credentials or response metadata in the repo.
func TestLive(t *testing.T) {
	token := os.Getenv("THEPORNDB_TOKEN")
	if token == "" {
		t.Skip("THEPORNDB_TOKEN not set")
	}
	c, err := New(Options{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.SearchScenes(context.Background(), url.Values{"per_page": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Data) == 0 {
		t.Fatal("expected results")
	}
	if _, err := c.GetScene(context.Background(), p.Data[0].ID, nil); err != nil {
		t.Fatal(err)
	}
}
