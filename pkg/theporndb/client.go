// Package theporndb implements the authenticated ThePornDB REST query API.
package theporndb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.theporndb.net"

// Options configures a client. Only Token is required. Cache controls optional response caching.
type Options struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
	Cache      Cache
}

// Client is safe for concurrent use provided its HTTPClient is too.
type Client struct {
	token      string
	baseURL    string
	http       *http.Client
	cache      Cache
	cacheScope string
}

func New(opts Options) (*Client, error) {
	token := strings.TrimSpace(opts.Token)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("theporndb: a valid token is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("theporndb: invalid base URL")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{token: token, baseURL: strings.TrimRight(base, "/"), http: hc, cache: opts.Cache, cacheScope: fmt.Sprintf("%x", sha256.Sum256([]byte(base+"\x00"+token)))}, nil
}

// Response is the API's single-resource envelope.
type Response[T any] struct {
	Data T `json:"data"`
}

// Page contains a result page and its pagination metadata.
type Page[T any] struct {
	Data  []T                `json:"data"`
	Meta  Meta               `json:"meta"`
	Links map[string]*string `json:"links"`
}

// Error reports an unsuccessful HTTP response. RetryAfter preserves the server header.
type Error struct {
	StatusCode int
	Message    string
	RetryAfter string
	Body       string
}

func (e *Error) Error() string { return fmt.Sprintf("theporndb: HTTP %d: %s", e.StatusCode, e.Message) }

func (c *Client) get(ctx context.Context, path string, query url.Values, dst any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	// Account state should stay fresh. Collection-changing GETs must reach the server.
	cacheable := c.cache != nil && !query.Has("add_to_collection") &&
		path != "/auth/user" && path != "/ratings" && path != "/favourites" && !strings.HasPrefix(path, "/user/")
	key := "v1:" + c.cacheScope + ":" + endpoint
	if cacheable {
		if data, ok := c.cache.Get(ctx, key); ok {
			var entry cachedResponse
			if json.Unmarshal(data, &entry) == nil && json.Valid(entry.Body) {
				if entry.Status == http.StatusNotFound {
					return responseError(entry.Status, entry.Body, "")
				}
				if entry.Status >= 200 && entry.Status < 300 {
					return decodeResponse(entry.Body, dst)
				}
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("theporndb: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("theporndb: request: %w", err)
	}
	defer res.Body.Close()

	limit := io.Reader(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		limit = io.LimitReader(res.Body, 64<<10)
	}
	body, err := io.ReadAll(limit)
	if err != nil {
		return fmt.Errorf("theporndb: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if cacheable && res.StatusCode == http.StatusNotFound && json.Valid(body) {
			data, _ := json.Marshal(cachedResponse{Status: res.StatusCode, Body: body})
			c.cache.Set(ctx, key, data)
		}
		return responseError(res.StatusCode, body, res.Header.Get("Retry-After"))
	}
	if err := decodeResponse(body, dst); err != nil {
		return err
	}
	if cacheable {
		data, _ := json.Marshal(cachedResponse{Status: res.StatusCode, Body: body})
		c.cache.Set(ctx, key, data)
	}
	return nil
}

func decodeResponse(body []byte, dst any) error {
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("theporndb: decode response: %w", err)
	}
	return nil
}
func responseError(status int, body []byte, retryAfter string) error {
	var problem struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &problem)
	if problem.Message == "" {
		problem.Message = http.StatusText(status)
	}
	return &Error{StatusCode: status, Message: problem.Message, Body: string(body), RetryAfter: retryAfter}
}
