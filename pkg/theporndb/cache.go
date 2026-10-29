package theporndb

import (
	"context"
	"encoding/json"
)

// Cache stores API responses. Implementations control expiry and must support
// concurrent calls. Keys identify the base URL, account, endpoint and query;
// credentials are hashed and never stored in plaintext in keys.
// A nil Options.Cache disables caching.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte)
}

type cachedResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
