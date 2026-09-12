package media

import (
	"testing"

	"github.com/lsongdev/files-go/model"
)

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

func TestParsedNameForEntryUsesSeriesDirectoryForNumberOnlyEpisode(t *testing.T) {
	parsed := ParsedNameForEntry(model.Entry{
		Name: "S01E03.mp4",
		Path: "Videos/TV Shows/Attack.On.Titan/S01/S01E03.mp4",
	}, true)
	if parsed.Title != "Attack On Titan" || parsed.Season == nil || *parsed.Season != 1 || parsed.Episode == nil || *parsed.Episode != 3 {
		t.Fatalf("directory-derived TV name = %#v", parsed)
	}
	nonTV := ParsedNameForEntry(model.Entry{Name: "S01E03.mp4", Path: "Documents/S01E03.mp4"}, false)
	if nonTV.Title != "" {
		t.Fatalf("non-TV name unexpectedly inferred from directory: %#v", nonTV)
	}
}
