package processor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/tmdb"
)

// MovieDisplay turns semantic movie/TV metadata into the common three-line
// presentation model. Lower-priority technical sources can still fill any
// line left empty here.
func MovieDisplay(kind string, parsed MovieName, candidate *tmdb.Candidate) (string, string, string) {
	year := parsed.Year
	if candidate != nil && candidate.Year != nil {
		year = candidate.Year
	}
	line1 := map[string]string{"movie": "电影", "tv": "电视剧", "episode": "剧集"}[kind]
	if line1 == "" {
		line1 = "影视"
	}
	if kind == "episode" && parsed.Season != nil && parsed.Episode != nil {
		line1 += fmt.Sprintf(" · S%02dE%02d", *parsed.Season, *parsed.Episode)
	}
	if year != nil {
		line1 += " · " + strconv.Itoa(*year)
	}

	releaseLine2 := joinDisplay(parsed.Release.Resolution, parsed.Release.Source)
	releaseLine3 := joinDisplay(parsed.Release.VideoCodec, parsed.Release.AudioCodec, parsed.Release.Version)
	line2 := releaseLine2
	line3 := releaseLine3
	if candidate != nil {
		original := strings.TrimSpace(candidate.OriginalTitle)
		if original != "" && !strings.EqualFold(original, strings.TrimSpace(candidate.Title)) {
			line2 = original
		}
		if candidate.VoteAverage > 0 {
			line3 = joinDisplay(fmt.Sprintf("TMDB %.1f", candidate.VoteAverage), releaseLine3)
		}
	}
	return line1, line2, line3
}
