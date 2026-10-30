package media

import (
	"path"
	"testing"

	"github.com/lsongdev/files-go/store"
)

// The manual match search starts from what identification parsed from names,
// never from the possibly wrong title it settled on.
func TestQueryFollowsNameParsing(t *testing.T) {
	chain := func(p string) (store.File, []store.File) {
		ancestors := []store.File{{Dir: true}} // the root
		for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
			ancestors = append([]store.File{{Name: path.Base(dir), Path: dir, Dir: true}}, ancestors...)
		}
		// Ancestors run from the root down; move the root to the front.
		ancestors = append([]store.File{ancestors[len(ancestors)-1]}, ancestors[:len(ancestors)-1]...)
		return store.File{Name: path.Base(p), Path: p}, ancestors
	}
	for _, tc := range []struct {
		path, hint, base, provider, want string
		dir                              bool
	}{
		{"Split.2016/Split.2016.720p.WEB-DL.XviD.AC3-FGT.avi", "movies", "", "tmdb", "Split", false},
		{"Shawshank/001.肖申克的救赎.1080p.mkv", "movies", "", "tmdb", "肖申克的救赎", false},
		{"Movie (2010)/CD1.mkv", "movies", "", "tmdb", "Movie", false},
		{"Walking.Dead/S01/The.Walking.Dead.S01E01.mkv", "tv", "", "tmdb", "Walking Dead", false},
		{"Walking.Dead/S01", "tv", "", "tmdb", "Walking Dead", true},
		{"Shows/Other.Show.S02E03.mkv", "", "", "tmdb", "Other Show", false},
		{"Box/Show.Name/S02", "", "", "tmdb", "Show Name", true},
		{"ABP/ABP-119/ABP-119-C.mp4", "jav", "", "theporndb", "ABP-119", false},
		{"ABP/ABP-119/movie.mp4", "jav", "", "theporndb", "ABP-119", false},
	} {
		f, ancestors := chain(tc.path)
		f.Dir = tc.dir
		if got := Query(f, ancestors, tc.hint, tc.base, tc.provider); got != tc.want {
			t.Errorf("Query(%s) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
