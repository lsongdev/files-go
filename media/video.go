package media

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/tmdb-go"
)

// Library identifies movies and TV episodes from names, folder structure,
// NFO files and TMDB. Hint returns the configured media type for a file and
// the folder that type applies from (show folders sit directly below it).
type Library struct {
	Store *store.Store
	TMDB  *TMDB // nil disables online lookups
	Path  Resolver
	Hint  func(store.File) (hint, base string)
	mu    sync.Mutex
	shows map[string]*showFlight
}

// Videos returns the enricher for video files; it runs after Probe.
func (l *Library) Videos() Enricher { return videos{l} }

// Folders aggregates file results and local artwork without catalog requests.
func (l *Library) Folders() Enricher { return folders{l} }

type videos struct{ *Library }

func (videos) Match(f *store.File) bool { return f.Kind == "video" }

func (v videos) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	if m.Kind != "video" || isExtra(f.Name) {
		return nil // not playable, or a sample/trailer next to the real thing
	}
	hint, base := v.Hint(*f)
	if (hint == "jav" && !m.Locked) || (m.Match != nil && m.Match.Provider != "tmdb") {
		return nil
	}
	n := ParseName(f.Name)
	m.Set("Release", strings.Join(nonEmpty(n.Resolution, n.Source, n.Codec), " · "))
	if !m.Locked {
		if sidecar, err := v.Store.ChildByName(ctx, f.Parent, strings.TrimSuffix(f.Name, path.Ext(f.Name))+".nfo"); err == nil {
			if err := (folders{v.Library}).readNFO(sidecar, m); err != nil {
				return err
			}
			// Keep the filename as the independent search fallback if the
			// sidecar's identifier contradicts the local runtime.
		}
	}
	// A directory NFO is shared input, consumed by its videos. Directories
	// themselves never resolve catalog identities.
	if !m.Locked && !strings.HasPrefix(m.providerID("tmdb"), "tv/") && hint != "tv" {
		if nfo, err := v.Store.ChildByName(ctx, f.Parent, "movie.nfo"); err == nil && m.Match == nil {
			if err := (folders{v.Library}).readNFO(nfo, m); err != nil {
				return err
			}
		}
	}
	if n.Year == 0 && hint == "movies" && parentPath(f.Path) != base && m.Match == nil && genericMovieName.MatchString(n.Title) {
		parent := ParseName(path.Base(parentPath(f.Path)))
		if parent.Title != "" {
			n.Title, n.Year = parent.Title, parent.Year
		}
	}
	if err := v.artwork(ctx, f.Parent, m); err != nil {
		return err
	}
	lockedMovie, lockedShow := strings.HasPrefix(m.providerID("tmdb"), "movie/"), strings.HasPrefix(m.providerID("tmdb"), "tv/")
	switch {
	case !lockedMovie && (lockedShow || hint == "tv" || n.Season > 0 && n.Episode > 0):
		return v.episode(ctx, f, m, n, hint, base)
	case lockedMovie || hint == "movies" || (hint == "" || hint == "auto") && n.LooksLikeRelease():
		item, err := v.find(ctx, m, "movie", n.Title, n.Year, evidence{Duration: m.Duration})
		if err != nil {
			return err
		}
		if item == nil && hint != "movies" && !lockedMovie {
			return nil
		}
		m.Kind, m.Title, m.Year = "movie", first(m.Title, n.Title), max(m.Year, n.Year)
		if item != nil {
			apply(m, item)
		} else {
			m.Review = "unmatched"
		}
	}
	return nil
}

func (v videos) episode(ctx context.Context, f *store.File, m *Meta, n Name, hint, base string) error {
	ancestors, err := v.Store.Ancestors(ctx, f.ID)
	if err != nil {
		return err
	}
	season, episode := n.Season, n.Episode
	if episode == 0 {
		episode, _ = EpisodeNumber(f.Name)
	}
	if episode == 0 && !strings.HasPrefix(m.providerID("tmdb"), "tv/") {
		return nil
	}
	var show *store.File
	for i := len(ancestors) - 1; i >= 0; i-- {
		a := ancestors[i]
		if s, ok := SeasonNumber(a.Name); ok && season == 0 {
			season = s
		}
		if hint == "tv" && parentPath(a.Path) == base && a.Path != base {
			show = &ancestors[i]
		}
	}
	if season == 0 {
		season = 1
	}
	title, year := n.Title, 0
	if show != nil {
		parsed := ParseName(show.Name)
		title, year = parsed.Title, parsed.Year
	}
	if title == "" {
		return nil
	}
	m.Kind, m.Season, m.Episode, m.Show = "episode", season, episode, title
	work := &Meta{Match: m.Match, Locked: m.Locked}
	var ev evidence
	if show != nil {
		if !work.Locked {
			if nfo, e := v.Store.ChildByName(ctx, show.ID, "tvshow.nfo"); e == nil {
				if err := (folders{v.Library}).readNFO(nfo, work); err != nil {
					return err
				}
				title, year = first(work.Title, title), max(work.Year, year)
			}
		}
		ev, err = v.showEvidence(ctx, *show)
		if err != nil {
			return err
		}
	}
	item, review, err := v.resolveShow(ctx, work, title, year, ev)
	if err != nil {
		return err
	}
	m.Series = &Series{Title: title, Year: year, Review: review}
	if item == nil {
		m.Review = review
		return nil
	}
	m.Match = &store.Match{Provider: "tmdb", Type: "tv", ID: strconv.Itoa(item.ID)}
	m.Show = item.Title
	m.Series = &Series{Title: item.Title, Original: item.Original, Year: item.Year, Summary: item.Overview,
		Rating: item.Rating, Genres: item.Genres, Review: review}
	if item.Poster != "" {
		m.Series.Poster = "tmdb:" + item.Poster
	}
	if item.Backdrop != "" {
		m.Series.Backdrop = "tmdb:" + item.Backdrop
	}
	if show != nil {
		art := &Meta{Poster: m.Series.Poster, Backdrop: m.Series.Backdrop}
		if err := v.artwork(ctx, show.ID, art); err != nil {
			return err
		}
		m.Series.Poster, m.Series.Backdrop = art.Poster, art.Backdrop
	}
	m.Poster, m.Backdrop, m.Genres = m.Series.Poster, m.Series.Backdrop, item.Genres
	if show == nil {
		m.Review = review
	}
	if episode == 0 {
		m.Title, m.Summary = item.Title, item.Overview
		return nil
	}
	ep, err := v.TMDB.Episode(ctx, item.ID, season, episode)
	if tmdb.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return transient(err)
	}
	m.Title, m.Summary, m.Rating, m.Year = ep.Name, ep.Overview, ep.VoteAverage, leadingYear(ep.AirDate)
	if ep.StillPath != "" {
		m.Poster = "tmdb:" + ep.StillPath
	}
	return nil
}

type folders struct{ *Library }

func (folders) Match(f *store.File) bool { return f.Dir && f.Path != "" }

var (
	posterNames   = []string{"poster.jpg", "folder.jpg", "cover.jpg", "poster.png", "folder.png", "cover.png", "front.jpg", "poster.webp"}
	backdropNames = []string{"fanart.jpg", "backdrop.jpg", "background.jpg", "fanart.png", "backdrop.png"}
)

func (d folders) readNFO(nfo store.File, m *Meta) error {
	name, err := d.Path(nfo)
	if err != nil {
		return err
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	var doc struct {
		XMLName  xml.Name
		Title    string `xml:"title"`
		Original string `xml:"originaltitle"`
		Year     string `xml:"year"`
		Plot     string `xml:"plot"`
		TMDBID   string `xml:"tmdbid"`
		Unique   []struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"uniqueid"`
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return err
	}
	if err := xml.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &doc); err != nil {
		return nil // NFO files are often plain text links; ignore them
	}
	if doc.XMLName.Local != "movie" && doc.XMLName.Local != "tvshow" {
		return nil
	}
	m.Title, m.Original, m.Summary = strings.TrimSpace(doc.Title), strings.TrimSpace(doc.Original), strings.TrimSpace(doc.Plot)
	m.Year = leadingYear(doc.Year)
	id := doc.TMDBID
	for _, u := range doc.Unique {
		if strings.EqualFold(u.Type, "tmdb") {
			id = u.Value
		}
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(id)); n > 0 && !m.Locked {
		id = strconv.Itoa(n)
		kind := map[bool]string{true: "tv", false: "movie"}[doc.XMLName.Local == "tvshow"]
		m.setMatch("tmdb", kind, id) // trusted unless the local files disagree
	}
	return nil
}

// evidence is what the local files say about a title, used to tell apart
// candidates with the same name (the anime and the live-action One Piece).
type evidence struct {
	Duration   float64 // seconds of the main video, movies
	Episodes   int     // episode files, shows
	MaxEpisode int     // highest episode number, shows
	Seasons    int     // highest season number, shows
}

func (e evidence) empty() bool { return e == evidence{} }

// fit scores how well a candidate's details agree with the evidence.
func (e evidence) fit(it Item) float64 {
	switch {
	case e.Duration > 0 && it.Runtime > 0:
		switch diff := abs(int(e.Duration/60) - it.Runtime); {
		case diff <= 10:
			return .15
		case diff > 30:
			return -.3
		}
	case e.Episodes > 0 && it.Episodes > 0:
		slack := max(2, it.Episodes/10)
		switch {
		case e.Episodes > it.Episodes+slack, e.MaxEpisode > it.Episodes+slack, e.Seasons > it.Seasons:
			return -.3 // more local episodes or seasons than the show has
		case abs(e.Episodes-it.Episodes) <= slack:
			return .2 // a complete local copy
		}
	}
	return 0
}

// popularity breaks ties between equally named titles, at most 0.1.
func popularity(p float64) float64 { return min(.1, math.Log10(1+p)/20) }

// find resolves a TMDB item: a locked or known id is fetched directly,
// otherwise the best search result scoring at least 0.8 on title, year,
// popularity and local evidence. Close runners-up mark the match for review.
func (l *Library) find(ctx context.Context, m *Meta, kind, title string, year int, ev evidence) (*Item, error) {
	if l.TMDB == nil {
		return nil, nil
	}
	if id, ok := strings.CutPrefix(m.providerID("tmdb"), kind+"/"); ok {
		n, _ := strconv.Atoi(id)
		item, err := l.TMDB.Details(ctx, kind, n)
		switch {
		case err == nil && (m.Locked || ev.fit(item) >= 0):
			return &item, nil
		case err == nil:
			// An id from an NFO that the local files contradict, e.g. a
			// scraper that picked the live-action One Piece for the anime.
			m.Review = "conflict"
		case m.Locked && tmdb.IsNotFound(err):
			m.Review = "unmatched"
			return nil, nil
		case !tmdb.IsNotFound(err):
			return nil, transient(err)
		}
		m.Match, m.Locked = nil, false // stale or wrong: search instead
	}
	if title == "" {
		return nil, nil
	}
	candidates, err := l.rank(ctx, kind, title, year, ev)
	if err == nil && year > 0 && (len(candidates) == 0 || candidates[0].score < .8) {
		// The year in names and NFOs is often off; try again without it.
		candidates, err = l.rank(ctx, kind, title, 0, ev)
	}
	if err != nil {
		return nil, transient(err)
	}
	if len(candidates) == 0 || candidates[0].score < .8 {
		return nil, nil
	}
	if m.Review == "" && len(candidates) > 1 && candidates[1].score >= .8 && candidates[0].score-candidates[1].score < .05 {
		m.Review = "ambiguous"
	}
	return &candidates[0].item, nil
}

type candidate struct {
	item  Item
	score float64
}

// rank searches TMDB and orders the results by score, best first.
func (l *Library) rank(ctx context.Context, kind, title string, year int, ev evidence) ([]candidate, error) {
	items, err := l.TMDB.Search(ctx, kind, title, year)
	if err != nil {
		return nil, err
	}
	var candidates []candidate
	for _, it := range items {
		score := matchScore(title, year, append([]string{it.Title, it.Original}, it.Aliases...), it.Year) + popularity(it.Popularity)
		if score >= .55 { // partial title matches may still win on evidence
			candidates = append(candidates, candidate{it, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	// Details carry runtime and episode counts; fetch them for the leading
	// candidates when there is evidence to compare, else only for the best.
	n := 1
	if !ev.empty() {
		n = 4
	}
	top := candidates[:min(n, len(candidates))]
	for i := range top {
		details, err := l.TMDB.Details(ctx, kind, top[i].item.ID)
		if err != nil {
			return nil, err
		}
		top[i].item, top[i].score = details, top[i].score+ev.fit(details)
	}
	sort.SliceStable(top, func(i, j int) bool { return top[i].score > top[j].score })
	return candidates, nil
}

// showEvidence counts the episode files below a show folder.
func (l *Library) showEvidence(ctx context.Context, show store.File) (evidence, error) {
	files, err := l.Store.VideosUnder(ctx, show)
	if err != nil {
		return evidence{}, err
	}
	var ev evidence
	parts := map[string]bool{}
	for _, f := range files {
		n := ParseName(f.Name)
		if n.Episode == 0 {
			n.Episode, _ = EpisodeNumber(f.Name)
		}
		if n.Season == 0 {
			n.Season, _ = SeasonNumber(path.Base(parentPath(f.Path)))
		}
		if n.Episode > 0 && !isExtra(f.Name) {
			// Split clips share one episode; their leading numbers are a
			// playlist order, not the show's episode numbering.
			if part := splitEpisode.FindStringSubmatch(strings.TrimSuffix(f.Name, path.Ext(f.Name))); part != nil {
				key := normalize(part[1])
				if !parts[key] {
					parts[key] = true
					ev.Episodes++
				}
			} else {
				ev.Episodes++
			}
			if explicit := ParseName(f.Name); explicit.Episode > 0 || episodeOnly.MatchString(strings.TrimSuffix(f.Name, path.Ext(f.Name))) {
				ev.MaxEpisode = max(ev.MaxEpisode, n.Episode)
			}
			ev.Seasons = max(ev.Seasons, n.Season)
		}
	}
	return ev, nil
}

var (
	genericMovieName = regexp.MustCompile(`(?i)^(?:movie|video|main|feature|(?:cd|disc|disk|part)[ ._-]*[0-9]+|[0-9]+)$`)
	splitEpisode     = regexp.MustCompile(`(?i)(?:^|-)\s*([^-]+?)\s*-\s*Part\s+\d+$`)
	moviePart        = regexp.MustCompile(`(?i)[ ._-]*(?:cd|disc|disk|part|pt)[ ._-]*\d+$`)
	extra            = regexp.MustCompile(`(?i)(?:^|[ ._()\[\]-])(?:sample|trailer|featurette|preview|花絮|预告)(?:$|[ ._()\[\]-])`)
	musicVideoLabel  = regexp.MustCompile(`(?i)\[mv\]|\(mv\)`)
)

func isExtra(name string) bool {
	stem := strings.TrimSuffix(name, path.Ext(name))
	return extra.MatchString(stem) || musicVideoLabel.MatchString(stem)
}

func apply(m *Meta, it *Item) {
	m.Title, m.Year, m.Summary, m.Rating, m.Genres = it.Title, it.Year, it.Overview, it.Rating, it.Genres
	m.setMatch("tmdb", it.Type, strconv.Itoa(it.ID))
	if it.Original != it.Title {
		m.Original = it.Original
	}
	if it.Poster != "" && (m.Poster == "" || m.Poster == "self") {
		m.Poster = "tmdb:" + it.Poster
	}
	if it.Backdrop != "" && m.Backdrop == "" {
		m.Backdrop = "tmdb:" + it.Backdrop
	}
	if it.Runtime > 0 {
		m.Set("Runtime", fmt.Sprintf("%d min", it.Runtime))
	}
}

func transient(err error) error {
	if tmdb.IsTemporary(err) {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return err
}

func parentPath(p string) string {
	if dir := path.Dir(p); dir != "." {
		return dir
	}
	return ""
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
