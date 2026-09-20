package processor

import (
	"testing"

	"github.com/lsongdev/files-go/model"
)

func TestParseMovieAndTVNames(t *testing.T) {
	movie := ParseMovieName("Interstellar.2014.BluRay.1080p.DTS.x264-CHD.mkv")
	if movie.Title != "Interstellar" || movie.Year == nil || *movie.Year != 2014 || movie.Season != nil {
		t.Fatalf("movie = %#v", movie)
	}
	tv := ParseMovieName("The.Last.of.Us.S01E03.2160p.WEB-DL.mkv")
	if tv.Title != "The Last of Us" || tv.Season == nil || *tv.Season != 1 || tv.Episode == nil || *tv.Episode != 3 {
		t.Fatalf("TV = %#v", tv)
	}
	legacy := ParseMovieName("Firefly.1x07.720p.mkv")
	if legacy.Title != "Firefly" || legacy.Season == nil || *legacy.Season != 1 || legacy.Episode == nil || *legacy.Episode != 7 {
		t.Fatalf("legacy TV = %#v", legacy)
	}
}

func TestParseMovieNameSeparatesFileinfoReleaseTagsBeforeTMDB(t *testing.T) {
	movie := ParseMovieName("Harry.Potter.and.the.Deathly.Hallows.Part.II.2011.1080p.BluRay.x264.DTS-WiKi.mkv")
	if movie.Title != "Harry Potter and the Deathly Hallows Part II" || movie.Year == nil || *movie.Year != 2011 ||
		movie.Release.Source != "BluRay" || movie.Release.Resolution != "1080p" || movie.Release.VideoCodec != "x264" || movie.Release.AudioCodec != "DTS" {
		t.Fatalf("fileinfo movie = %#v", movie)
	}
	tv := ParseMovieName("The.Mandalorian.S01E05.1080p.WEB-DL.DDP5.1.H.265-NTb.mp4")
	if tv.Title != "The Mandalorian" || tv.Season == nil || *tv.Season != 1 || tv.Episode == nil || *tv.Episode != 5 ||
		tv.Release.Source != "WEB-DL" || tv.Release.Resolution != "1080p" || tv.Release.AudioCodec != "DDP5.1" || tv.Release.VideoCodec != "H.265" {
		t.Fatalf("fileinfo TV = %#v", tv)
	}
}

func TestParseMovieEntryNameUsesSeriesDirectoryForNumberOnlyEpisode(t *testing.T) {
	parsed := ParseMovieEntryName(model.Entry{
		Name: "S01E03.mp4",
		Path: "Videos/TV Shows/Attack.On.Titan/S01/S01E03.mp4",
	}, true)
	if parsed.Title != "Attack On Titan" || parsed.Season == nil || *parsed.Season != 1 || parsed.Episode == nil || *parsed.Episode != 3 {
		t.Fatalf("directory-derived TV name = %#v", parsed)
	}
	nonTV := ParseMovieEntryName(model.Entry{Name: "S01E03.mp4", Path: "Documents/S01E03.mp4"}, false)
	if nonTV.Title != "" {
		t.Fatalf("non-TV name unexpectedly inferred from directory: %#v", nonTV)
	}
}

func TestParseMovieEntryNameSupportsCommonEpisodeOnlyConventions(t *testing.T) {
	tests := []struct {
		name, path, title string
		season, episode   int
	}{
		{"E001.Farm.mp4", "TV/Maisy.Mouse/E001.Farm.mp4", "Maisy Mouse", 1, 1},
		{"EP00 Introduction.mp4", "TV/Stranger.Talking.to.Jihadists/EP00 Introduction.mp4", "Stranger Talking to Jihadists", 1, 0},
		{"[site]人民的名义.DVD版.01.HD1080p.mp4", "TV/In.the.Name.of.the.People/[site]人民的名义.DVD版.01.HD1080p.mp4", "人民的名义", 1, 1},
		{"1.mp4", "TV/Zebra.English/S2/week1/1.mp4", "Zebra English", 2, 1},
	}
	for _, test := range tests {
		parsed := ParseMovieEntryName(model.Entry{Name: test.name, Path: test.path}, true)
		if parsed.Title != test.title || parsed.Season == nil || *parsed.Season != test.season || parsed.Episode == nil || *parsed.Episode != test.episode {
			t.Errorf("%s = %#v", test.path, parsed)
		}
	}
}
