package media

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Name is what a release-style filename says about a movie or episode.
type Name struct {
	Title      string
	Year       int
	Season     int // 0 when unknown
	Episode    int // 0 when unknown
	Resolution string
	Source     string
	Codec      string
}

var (
	episodePattern = regexp.MustCompile(`(?i)(?:\bS(\d{1,2})[ ._-]*E(\d{1,3})\b|\b(\d{1,2})x(\d{1,3})\b)`)
	yearPattern    = regexp.MustCompile(`(?:^|[\s(\[])((?:19|20)\d{2})(?:$|[\s)\]])`)
	bracketPattern = regexp.MustCompile(`\[[^\]]*\]|【[^】]*】`)
	spacePattern   = regexp.MustCompile(`\s+`)
	junkPattern    = regexp.MustCompile(`(?i)\b(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|hdr10?|dv|blu-?ray|bdrip|brrip|web-?dl|webrip|hdtv|dvdrip|remux|x26[45]|h ?26[45]|hevc|avc|av1|dts(?:-?hd)?|truehd|atmos|ddp?5 ?1|aac|ac3|flac|10bit|proper|repack|extended|remastered|unrated)\b`)
	resolutionTag  = regexp.MustCompile(`(?i)\b(2160p|1080p|1080i|720p|576p|480p|4k)\b`)
	sourceTag      = regexp.MustCompile(`(?i)\b(blu-?ray|bdrip|web-?dl|webrip|hdtv|dvdrip|remux|uhd)\b`)
	codecTag       = regexp.MustCompile(`(?i)\b(x26[45]|h\.?26[45]|hevc|av1)\b`)
	seasonDir      = regexp.MustCompile(`(?i)^(?:s|season)[ ._-]*(\d{1,2})$|^第\s*(\d{1,2})\s*季$`)
	episodeOnly    = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:E|EP)[ ._-]?(\d{1,3})(?:$|[ ._-])|^0*(\d{1,3})$|第\s*(\d{1,3})\s*集`)
)

// ParseName extracts title, year, season/episode and release tags from a
// file or folder name.
func ParseName(filename string) Name {
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	if kindOf[Ext(filename)] == "" {
		stem = filename // folder names like "Mr. Robot" have no extension
	}
	n := Name{
		Resolution: firstMatch(resolutionTag, stem),
		Source:     firstMatch(sourceTag, stem),
		Codec:      firstMatch(codecTag, stem),
	}
	s := bracketPattern.ReplaceAllString(stem, " ")
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	if m := episodePattern.FindStringSubmatchIndex(s); m != nil {
		var season, episode string
		if m[2] >= 0 {
			season, episode = s[m[2]:m[3]], s[m[4]:m[5]]
		} else {
			season, episode = s[m[6]:m[7]], s[m[8]:m[9]]
		}
		n.Season, _ = strconv.Atoi(season)
		n.Episode, _ = strconv.Atoi(episode)
		s = s[:m[0]]
	}
	// The last year wins: "Blade Runner 2049 (2017)".
	if all := yearPattern.FindAllStringSubmatchIndex(s, -1); len(all) > 0 && all[len(all)-1][2] > 0 {
		m := all[len(all)-1]
		n.Year, _ = strconv.Atoi(s[m[2]:m[3]])
		s = s[:m[2]]
	}
	if m := junkPattern.FindStringIndex(s); m != nil {
		s = s[:m[0]]
	}
	n.Title = spacePattern.ReplaceAllString(strings.Trim(s, " -._()[]"), " ")
	return n
}

// LooksLikeRelease reports whether a name carries enough release structure
// to be worth a movie lookup outside a movies folder.
func (n Name) LooksLikeRelease() bool {
	return n.Title != "" && n.Year > 0 && (n.Resolution != "" || n.Source != "" || n.Codec != "")
}

// SeasonNumber parses folder names like "Season 2", "S02" or "第2季".
func SeasonNumber(dir string) (int, bool) {
	m := seasonDir.FindStringSubmatch(strings.TrimSpace(dir))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1] + m[2])
	return n, true
}

// EpisodeNumber parses names that carry only an episode number,
// e.g. "E05.mkv", "05.mp4" or "第5集.mp4".
func EpisodeNumber(filename string) (int, bool) {
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	m := episodeOnly.FindStringSubmatch(stem)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1] + m[2] + m[3])
	return n, true
}

func firstMatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

var nonWord = regexp.MustCompile(`[^\pL\pN]+`)

func normalize(s string) string { return nonWord.ReplaceAllString(strings.ToLower(s), "") }

// matchScore rates how well a candidate title/year fits the parsed name.
// 0.8 is an exact normalized title match; the year adds or subtracts.
func matchScore(title string, year int, titles []string, candidateYear int) float64 {
	want, score := normalize(title), 0.0
	if want == "" {
		return 0
	}
	for _, t := range titles {
		got := normalize(t)
		switch {
		case got == "":
		case got == want:
			score = max(score, .8)
		case strings.Contains(got, want) || strings.Contains(want, got):
			score = max(score, .6)
		}
	}
	if year > 0 && candidateYear > 0 {
		switch d := abs(year - candidateYear); {
		case d == 0:
			score += .2
		case d == 1:
			score += .1
		default:
			score -= .3
		}
	}
	return score
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
