package media

import (
	"context"
	"time"

	"github.com/lsongdev/files-go/store"
)

// ThePornDBCache stores client responses in the index database for 30 days.
type ThePornDBCache struct{ Store *store.Store }

func (c ThePornDBCache) Get(ctx context.Context, key string) ([]byte, bool) {
	return c.Store.CacheGet(ctx, "theporndb:response:"+key, 30*24*time.Hour)
}
func (c ThePornDBCache) Set(ctx context.Context, key string, value []byte) {
	_ = c.Store.CachePut(ctx, "theporndb:response:"+key, value)
}
