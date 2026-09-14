package media

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/model"
)

type ParsedName struct {
	Title   string
	Year    *int
	Season  *int
	Episode *int
}

var (
	tvPattern      = regexp.MustCompile(`(?i)(?:\bS(\d{1,2})[ ._-]*E(\d{1,3})\b|\b(\d{1,2})x(\d{1,3})\b)`)
	seasonDir      = regexp.MustCompile(`(?i)^(?:s(?:eason)?[ ._-]*\d{1,2}|第[一二三四五六七八九十百0-9]+季)$`)
	seasonNumber   = regexp.MustCompile(`(?i)^s(?:eason)?[ ._-]*(\d{1,2})$`)
	episodeOnly    = regexp.MustCompile(`(?i)(?:^|[ ._-])E(?:P)?[ ._-]?(\d{1,3})(?:\b|[ ._-])`)
	numberOnly     = regexp.MustCompile(`^0*(\d{1,3})$`)
	trailingNumber = regexp.MustCompile(`(?:^|[ ._-])0*(\d{1,3})(?:[ ._-]|$)`)
	seriesGroupDir = regexp.MustCompile(`(?i)^(?:week|周|第)[ ._-]*[一二三四五六七八九十百0-9]+(?:周|集)?$`)
	yearPattern    = regexp.MustCompile(`(?:^|\s|[([])((?:19|20)\d{2})(?:$|\s|[)\]])`)
	bracketPattern = regexp.MustCompile(`\[[^\]]*\]`)
	spacePattern   = regexp.MustCompile(`\s+`)
	releaseToken   = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|576p|4k|uhd|bluray|blu-ray|web[ ._-]?dl|webrip|hdtv|dvdrip|remux|x26[45]|h[ ._-]?26[45]|hevc|av1|hdr10|hdr|dolby[ ._-]?vision|dts|aac|flac)\b`)
)

// ParsedNameForEntry uses the containing series directory when an episode is
// named only by its season/episode number, for example
// Attack.On.Titan/S01/S01E01.mp4. The filesystem hierarchy remains the source
// of the fallback title; no separate media browsing tree is introduced.
func ParsedNameForEntry(entry model.Entry, isTV bool) ParsedName {
	result := ParseName(entry.Name)
	if isTV && (result.Season == nil || result.Episode == nil) {
		if season, episode, ok := fallbackTVNumbers(entry); ok {
			result.Season, result.Episode = &season, &episode
			// These conventions normally omit the series name or include release
			// noise. The containing show folder is the stable identity source.
			result.Title = fallbackTVTitle(entry)
			result.Year = nil
		}
	}
	if !isTV || result.Season == nil || result.Episode == nil || result.Title != "" {
		return result
	}
	directory := filepath.Dir(filepath.ToSlash(entry.Path))
	name := filepath.Base(directory)
	if seasonDir.MatchString(name) {
		name = filepath.Base(filepath.Dir(directory))
	}
	if name == "." || name == "/" {
		return result
	}
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	result.Title = spacePattern.ReplaceAllString(strings.Trim(name, " -._()"), " ")
	return result
}

func fallbackTVTitle(entry model.Entry) string {
	stem := strings.TrimSuffix(filepath.Base(entry.Name), filepath.Ext(entry.Name))
	clean := bracketPattern.ReplaceAllString(stem, " ")
	clean = strings.NewReplacer(".", " ", "_", " ").Replace(clean)
	for _, match := range trailingNumber.FindAllStringSubmatchIndex(clean, -1) {
		value, _ := strconv.Atoi(clean[match[2]:match[3]])
		if value > 200 {
			continue
		}
		prefix := strings.TrimSpace(clean[:match[0]])
		prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "DVD版")
		prefix = strings.Trim(prefix, " -._()")
		if len([]rune(normalizedTVTitle(prefix))) >= 2 {
			return spacePattern.ReplaceAllString(prefix, " ")
		}
	}
	return fallbackSeriesTitle(entry.Path)
}

func normalizedTVTitle(value string) string {
	return nonTitle.ReplaceAllString(strings.ToLower(value), "")
}

func fallbackTVNumbers(entry model.Entry) (int, int, bool) {
	stem := strings.TrimSuffix(filepath.Base(entry.Name), filepath.Ext(entry.Name))
	episode := -1
	if match := episodeOnly.FindStringSubmatch(stem); match != nil {
		episode, _ = strconv.Atoi(match[1])
	} else if match := numberOnly.FindStringSubmatch(stem); match != nil {
		episode, _ = strconv.Atoi(match[1])
	} else {
		for _, match := range trailingNumber.FindAllStringSubmatch(stem, -1) {
			value, _ := strconv.Atoi(match[1])
			if value <= 200 {
				episode = value
			}
		}
	}
	if episode < 0 {
		return 0, 0, false
	}
	season := 1
	for directory := filepath.Dir(filepath.ToSlash(entry.Path)); directory != "." && directory != "/"; directory = filepath.Dir(directory) {
		if match := seasonNumber.FindStringSubmatch(filepath.Base(directory)); match != nil {
			season, _ = strconv.Atoi(match[1])
			break
		}
	}
	return season, episode, true
}

func fallbackSeriesTitle(path string) string {
	directory := filepath.Dir(filepath.ToSlash(path))
	for directory != "." && directory != "/" {
		name := filepath.Base(directory)
		if !seasonDir.MatchString(name) && !seriesGroupDir.MatchString(name) && !numberOnly.MatchString(name) {
			name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
			return spacePattern.ReplaceAllString(strings.Trim(name, " -._()"), " ")
		}
		directory = filepath.Dir(directory)
	}
	return ""
}

func ParseName(filename string) ParsedName {
	name := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	name = bracketPattern.ReplaceAllString(name, " ")
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	result := ParsedName{}
	if match := tvPattern.FindStringSubmatch(name); match != nil {
		seasonText, episodeText := match[1], match[2]
		if seasonText == "" {
			seasonText, episodeText = match[3], match[4]
		}
		season, _ := strconv.Atoi(seasonText)
		episode, _ := strconv.Atoi(episodeText)
		result.Season, result.Episode = &season, &episode
		name = name[:tvPattern.FindStringIndex(name)[0]]
	}
	if match := yearPattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[1])
		result.Year = &year
		name = name[:yearPattern.FindStringIndex(name)[0]]
	}
	if index := releaseToken.FindStringIndex(name); index != nil {
		name = name[:index[0]]
	}
	name = strings.Trim(name, " -._()")
	result.Title = spacePattern.ReplaceAllString(name, " ")
	return result
}
