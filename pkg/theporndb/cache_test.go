package theporndb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type testCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (c *testCache) Get(_ context.Context, k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[k]
	return v, ok
}
func (c *testCache) Set(_ context.Context, k string, v []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = map[string][]byte{}
	}
	c.data[k] = v
}

func TestClientCache(t *testing.T) {
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{"data":[]}`)) }))
	defer s.Close()
	cache := &testCache{}
	c, _ := New(Options{Token: "secret-token", BaseURL: s.URL, Cache: cache})
	ctx := context.Background()
	for range 2 {
		if _, err := c.SearchJAV(ctx, url.Values{"q": {"CODE-123"}, "page": {"1"}}); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Fatalf("cache miss: %d requests", hits)
	}
	for key := range cache.data {
		if strings.Contains(key, "secret-token") {
			t.Fatal("token leaked into cache key")
		}
	}
	c.SearchJAV(ctx, url.Values{"page": {"2"}, "q": {"CODE-123"}})
	c.SearchMovies(ctx, url.Values{"q": {"CODE-123"}, "page": {"1"}})
	other, _ := New(Options{Token: "other-token", BaseURL: s.URL, Cache: cache})
	other.SearchJAV(ctx, url.Values{"q": {"CODE-123"}, "page": {"1"}})
	if hits != 4 {
		t.Fatalf("requests collided: %d", hits)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.SearchJAV(canceled, url.Values{"q": {"CODE-123"}, "page": {"1"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestCacheResponses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantHits int
	}{
		{"success", 200, `{"data":{"id":"id"}}`, 1},
		{"missing", 404, `{"message":"missing"}`, 1},
		{"unauthorized", 401, `{"message":"unauthorized"}`, 2},
		{"rate limit", 429, `{"message":"slow down"}`, 2},
		{"server failure", 503, `{"message":"unavailable"}`, 2},
		{"malformed", 200, `{"data":`, 2},
		{"invalid type", 200, `{"data":{"id":42}}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c, _ := New(Options{Token: "test", BaseURL: s.URL, Cache: &testCache{}})
			for range 2 {
				_, err := c.GetJAV(context.Background(), "id", nil)
				if tc.status == 404 {
					var e *Error
					if !errors.As(err, &e) || e.StatusCode != 404 || e.Message != "missing" {
						t.Fatalf("cached error: %v", err)
					}
				}
			}
			if hits != tc.wantHits {
				t.Fatalf("%d requests, want %d", hits, tc.wantHits)
			}
		})
	}
}

func TestCacheBypass(t *testing.T) {
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{}`)) }))
	defer s.Close()
	c, _ := New(Options{Token: "test", BaseURL: s.URL, Cache: &testCache{}})
	for range 2 {
		c.GetJAV(context.Background(), "id", url.Values{"add_to_collection": {"true"}})
		c.Me(context.Background(), nil)
		c.GetRating(context.Background(), nil)
	}
	if hits != 6 {
		t.Fatalf("stateful requests cached: %d", hits)
	}
}
