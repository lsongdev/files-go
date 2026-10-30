package media

import (
	"strings"
	"testing"
)

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
		{"071.低俗小说.1994.1080p.mkv", "低俗小说", 1994, 0, 0, "1080p", ""},
		{"001.肖申克的救赎.1080p.mkv", "肖申克的救赎", 0, 0, 0, "1080p", ""},
		{"Blade.Runner.DC.2011.HR-HDTV.mkv", "Blade Runner", 2011, 0, 0, "", "HDTV"},
		{"Mission.Impossible.Rogue.Nation.HD.720p.rmvb", "Mission Impossible Rogue Nation", 0, 0, 0, "720p", ""},
		{"DC.League.of.Super-Pets.2022.mkv", "DC League of Super-Pets", 2022, 0, 0, "", ""},
		{"The.Legend.of.1900.1998.1080p.mkv", "The Legend of 1900", 1998, 0, 0, "1080p", ""},
		{"Rage.2016-720p.mp4", "Rage", 2016, 0, 0, "720p", ""},
		{"One.Flew.Over.the.Cuckoo's.Nest-1975-1976.mkv", "One Flew Over the Cuckoo's Nest", 1975, 0, 0, "", ""},
		{"Peppa.Pig.S07E01E02E03E04E05.1080p.NICK.WEB-DL.AAC2.0.x264-BTN.mp4", "Peppa Pig", 0, 7, 1, "1080p", "WEB-DL"},
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
	for name, want := range map[string]int{"Season 2": 2, "S03": 3, "第4季": 4, "season_10": 10,
		"生活大爆炸 第十季BT全": 10, "第二十三季": 23, "Show.S03.1080p.BluRay": 3, "Show Season 4 [1080p]": 4} {
		if got, ok := SeasonNumber(name); !ok || got != want {
			t.Errorf("SeasonNumber(%q) = %d, %v", name, got, ok)
		}
	}
	for _, name := range []string{"Sherlock S1-S4", "Show S01 & S02", "Season 1-4", "Better.Call.Saul.S02E06.1080p", "Mr. Robot", "Fear the Walking Dead"} {
		if n, ok := SeasonNumber(name); ok {
			t.Errorf("SeasonNumber(%q) = %d, want none", name, n)
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
	for want, titles := range map[string][]string{
		"Walking Dead":          {"The Walking Dead"},
		"Love Death and Robots": {"Love, Death & Robots"},
		"Kuiba II":              {"Kuiba 2"},
		"Paranormal Activity 1": {"Paranormal Activity"},
	} {
		if s := matchScore(want, 0, titles, 0); s != .8 {
			t.Errorf("matchScore(%q, %q) = %v", want, titles, s)
		}
	}
	if s := matchScore("Dragon", 0, []string{"Dragonheart 3: The Sorcerer's Curse"}, 0); s >= .55 {
		t.Errorf("a short title inside a long one = %v", s)
	}
	if got := queryVariants("DespicableMe II"); strings.Join(got, "|") != "DespicableMe II|Despicable Me II|Despicable Me 2" {
		t.Errorf("queryVariants = %q", got)
	}
	if got := queryVariants("Mad Detective (Sun taam"); got[1] != "Mad Detective" {
		t.Errorf("queryVariants = %q", got)
	}
	if s := matchScore("Dune", 2021, []string{"Dune"}, 1984); s >= .8 {
		t.Errorf("wrong year accepted: %v", s)
	}
}

func TestIsExtra(t *testing.T) {
	for name, want := range map[string]bool{"Sample.mkv": true, "movie-sample.mkv": true, "Dune.Trailer.mp4": true, "预告.mp4": true,
		"Dune.2021.mkv": false, "Samples.of.Life.mkv": false} {
		if isExtra(name) != want {
			t.Errorf("isExtra(%q) = %v", name, !want)
		}
	}
	if !isExtra("Saw II Unrated (Sample).mkv") {
		t.Error("parenthesized sample not excluded")
	}
	if !isExtra("One More Time (2007) [MV].mkv") || isExtra("MV.2020.mkv") {
		t.Error("only an explicit MV label should skip movie matching")
	}
}
