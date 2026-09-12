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
