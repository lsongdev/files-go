package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// Synthetic indexed rows, not a claim about NAS filesystem performance.
func BenchmarkSearch(b *testing.B) {
	for _, size := range []int{100000, 1000000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			st, e := Open(filepath.Join(b.TempDir(), "search.db"))
			if e != nil {
				b.Fatal(e)
			}
			defer st.Close()
			_, e = st.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
   INSERT INTO files(root,path,name,title,kind) SELECT 'r',printf('%d',x),printf('作品-%d.mkv',x),CASE WHEN x%100=0 THEN '音乐精选合集' ELSE '电影测试作品' END,'video' FROM n`, size)
			if e != nil {
				b.Fatal(e)
			}
			for _, q := range []string{"音乐", "精选合集", "作品"} {
				b.Run(q, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, e := st.Search(context.Background(), SearchOptions{Query: q}); e != nil {
							b.Fatal(e)
						}
					}
				})
			}
		})
	}
}
