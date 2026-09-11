package media

import "testing"

func TestParseMovieAndTVNames(t *testing.T) {
	movie := ParseName("Interstellar.2014.BluRay.1080p.DTS.x264-CHD.mkv")
	if movie.Title != "Interstellar" || movie.Year == nil || *movie.Year != 2014 || movie.Season != nil {
		t.Fatalf("movie = %#v", movie)
	}
	tv := ParseName("The.Last.of.Us.S01E03.2160p.WEB-DL.mkv")
	if tv.Title != "The Last of Us" || tv.Season == nil || *tv.Season != 1 || tv.Episode == nil || *tv.Episode != 3 {
		t.Fatalf("TV = %#v", tv)
	}
	legacy := ParseName("Firefly.1x07.720p.mkv")
	if legacy.Title != "Firefly" || legacy.Season == nil || *legacy.Season != 1 || legacy.Episode == nil || *legacy.Episode != 7 {
		t.Fatalf("legacy TV = %#v", legacy)
	}
}
