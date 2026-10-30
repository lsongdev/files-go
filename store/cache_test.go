package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCachePruneExpiresOnlyRequestedPrefix(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "cache.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	ctx := context.Background()
	for _, key := range []string{"theporndb:old", "theporndb:fresh", "other:old"} {
		if e := st.CachePut(ctx, key, []byte("response")); e != nil {
			t.Fatal(e)
		}
	}
	old := time.Now().Add(-48 * time.Hour).Unix()
	if _, e := st.db.Exec(`UPDATE cache SET at=? WHERE key IN ('theporndb:old','other:old')`, old); e != nil {
		t.Fatal(e)
	}
	if e := st.CachePrune(ctx, "theporndb:", 24*time.Hour); e != nil {
		t.Fatal(e)
	}
	for key, want := range map[string]bool{"theporndb:old": false, "theporndb:fresh": true, "other:old": true} {
		_, got := st.CacheGet(ctx, key, 7*24*time.Hour)
		if got != want {
			t.Fatalf("%s present=%t, want %t", key, got, want)
		}
	}
}
func BenchmarkCachePruneFreshLargeResponses(b *testing.B) {
	st, e := Open(filepath.Join(b.TempDir(), "cache.db"))
	if e != nil {
		b.Fatal(e)
	}
	defer st.Close()
	_, e = st.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100)
 INSERT INTO cache(key,value,at) SELECT printf('theporndb:%d',x),zeroblob(1048576),unixepoch() FROM n`)
	if e != nil {
		b.Fatal(e)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if e := st.CachePrune(context.Background(), "theporndb:", 30*24*time.Hour); e != nil {
			b.Fatal(e)
		}
	}
}
