package media

import "testing"

func TestParseName(t *testing.T) {
	tests := []struct {
		name            string
		title           string
		year, s, e      int
		resolution, src string
	}{
		{"Interstellar.2014.BluRay.1080p.DTS.x264-CHD.mkv", "Interstellar", 2014, 0, 0, "1080p", "BluRay"},
		{"The.Last.of.Us.S01E03.2160p.WEB-DL.mkv", "The Last of Us", 0, 1, 3, "2160p", "WEB-DL"},
		{"Firefly.1x07.720p.mkv", "Firefly", 0, 1, 7, "720p", ""},
		{"Harry.Potter.and.the.Deathly.Hallows.Part.II.2011.1080p.BluRay.x264.mkv", "Harry Potter and the Deathly Hallows Part II", 2011, 0, 0, "1080p", "BluRay"},
		{"Blade Runner 2049 (2017).mkv", "Blade Runner 2049", 2017, 0, 0, "", ""},
		{"[Group] 流浪地球 The Wandering Earth 2019 1080p.mp4", "流浪地球 The Wandering Earth", 2019, 0, 0, "1080p", ""},
		{"Mr. Robot", "Mr Robot", 0, 0, 0, "", ""},
		{"Miss.Hong.Kong.Pageant.2015.HD720P.X264.AAC.Cantonese.mp4", "Miss Hong Kong Pageant", 2015, 0, 0, "", ""},
	}
	for _, tt := range tests {
		n := ParseName(tt.name)
		if n.Title != tt.title || n.Year != tt.year || n.Season != tt.s || n.Episode != tt.e || n.Resolution != tt.resolution || n.Source != tt.src {
			t.Errorf("ParseName(%q) = %+v", tt.name, n)
		}
	}
}

func TestSeasonAndEpisodeNumbers(t *testing.T) {
	for name, want := range map[string]int{"Season 2": 2, "S03": 3, "第4季": 4, "season_10": 10} {
		if got, ok := SeasonNumber(name); !ok || got != want {
			t.Errorf("SeasonNumber(%q) = %d, %v", name, got, ok)
		}
	}
	if _, ok := SeasonNumber("Specials and Extras"); ok {
		t.Error("non-season folder parsed as season")
	}
	for name, want := range map[string]int{"E05.mkv": 5, "05.mp4": 5, "第12集.mp4": 12, "EP 3 Pilot.mkv": 3,
		"OnePiece.ep840.mp4": 840, "[www.domp4.cc]人民的名义.DVD版.01.HD1080p.mp4": 1, "Show 2019 07.mkv": 7} {
		if got, ok := EpisodeNumber(name); !ok || got != want {
			t.Errorf("EpisodeNumber(%q) = %d, %v", name, got, ok)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := map[string]string{
		"a.MKV": "video", "index.ts": "code", "types.d.ts": "code", "song.flac": "audio",
		"IMG_0001.HEIC": "image", "book.epub": "book", "setup.exe": "app", "x.apk": "app", "._a.mp4": "",
	}
	for name, want := range tests {
		if got := Classify(name, 100); got != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
	if Classify("clip.ts", 10<<20) != "video" {
		t.Error("large .ts should be a video candidate")
	}
}

func TestMatchScore(t *testing.T) {
	if s := matchScore("The Last of Us", 0, []string{"最后生还者", "The Last of Us"}, 2023); s < .8 {
		t.Errorf("alias match = %v", s)
	}
	if s := matchScore("Amelie", 0, []string{"Amélie"}, 2001); s < .8 {
		t.Errorf("accented title = %v", s)
	}
	if s := matchScore("In the Name of the People", 0, []string{"In the Name of People"}, 0); s != .6 {
		t.Errorf("near title = %v", s)
	}
	if s := matchScore("Alien", 0, []string{"Aliens"}, 0); s != .6 {
		t.Errorf("contained title = %v", s)
	}
	if s := matchScore("Dune", 2021, []string{"Dune"}, 1984); s >= .8 {
		t.Errorf("wrong year accepted: %v", s)
	}
}
