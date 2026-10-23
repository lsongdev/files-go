package media

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/files-go/tmdb"
)

// Library identifies movies and TV episodes from names, folder structure,
// NFO files and TMDB. Hint returns the configured media type for a file and
// the folder that type applies from (show folders sit directly below it).
type Library struct {
	Store *store.Store
	TMDB  *tmdb.Client // nil disables online lookups
	Path  Resolver
	Hint  func(store.File) (hint, base string)
}

// Videos returns the enricher for video files; it runs after Probe.
func (l *Library) Videos() Enricher { return videos{l} }

// Folders returns the enricher for directories: artwork, NFO, shows, movies.
func (l *Library) Folders() Enricher { return folders{l} }

type videos struct{ *Library }

func (videos) Match(f *store.File) bool { return f.Kind == "video" }

func (v videos) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	if m.Kind != "video" {
		return nil // not a playable video after probing
	}
	hint, base := v.Hint(*f)
	n := ParseName(f.Name)
	m.Set("Release", strings.Join(nonEmpty(n.Resolution, n.Source, n.Codec), " · "))
	lockedMovie, lockedShow := strings.HasPrefix(m.TMDB, "movie/"), strings.HasPrefix(m.TMDB, "tv/")
	switch {
	case !lockedMovie && (lockedShow || hint == "tv" || n.Season > 0 && n.Episode > 0):
		return v.episode(ctx, f, m, n, hint, base)
	case lockedMovie || hint == "movies" || (hint == "" || hint == "auto") && n.LooksLikeRelease():
		item, err := v.find(ctx, m, "movie", n.Title, n.Year)
		if err != nil {
			return err
		}
		if item == nil && hint != "movies" && !lockedMovie {
			return nil
		}
		m.Kind, m.Title, m.Year = "movie", n.Title, n.Year
		if item != nil {
			apply(m, item)
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
		if episode, _ = EpisodeNumber(f.Name); episode == 0 && !strings.HasPrefix(m.TMDB, "tv/") {
			return nil // a loose video in a TV folder, e.g. a trailer
		}
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
	if !m.Locked && show != nil {
		var shown Meta
		if json.Unmarshal(show.Meta, &shown) == nil && strings.HasPrefix(shown.TMDB, "tv/") {
			m.TMDB = shown.TMDB // reuse the show folder's (possibly manual) match
		}
	}
	item, err := v.find(ctx, m, "tv", title, year)
	if err != nil || item == nil {
		return err
	}
	m.Show, m.TMDB = item.Title, fmt.Sprintf("tv/%d", item.ID)
	m.Poster, m.Backdrop, m.Genres = "tmdb:"+item.Poster, "tmdb:"+item.Backdrop, item.Genres
	if item.Poster == "" {
		m.Poster = ""
	}
	if item.Backdrop == "" {
		m.Backdrop = ""
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
	m.Title, m.Summary, m.Rating, m.Year = ep.Name, ep.Overview, ep.Rating, leadingYear(ep.AirDate)
	if ep.Still != "" {
		m.Poster = "tmdb:" + ep.Still
	}
	return nil
}

type folders struct{ *Library }

func (folders) Match(f *store.File) bool { return f.Dir && f.Path != "" }

var (
	posterNames   = []string{"poster.jpg", "folder.jpg", "cover.jpg", "poster.png", "folder.png", "cover.png", "front.jpg", "poster.webp"}
	backdropNames = []string{"fanart.jpg", "backdrop.jpg", "background.jpg", "fanart.png", "backdrop.png"}
)

func (d folders) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	if p, err := d.Store.ChildByName(ctx, f.ID, posterNames...); err == nil {
		m.Poster = fmt.Sprintf("file:%d", p.ID)
	}
	if p, err := d.Store.ChildByName(ctx, f.ID, backdropNames...); err == nil {
		m.Backdrop = fmt.Sprintf("file:%d", p.ID)
	}
	hint, base := d.Hint(*f)
	n := ParseName(f.Name)
	kind := ""
	switch {
	case hint == "tv" && parentPath(f.Path) == base:
		kind = "tv"
	case hint == "movies" && parentPath(f.Path) == base:
		kind = "movie"
	case hint == "tv":
		if season, ok := SeasonNumber(f.Name); ok {
			m.Kind, m.Season = "season", season
			if m.Poster == "" {
				m.Poster = d.parentPoster(ctx, *f)
			}
		}
		return nil
	}
	if nfo, err := d.Store.ChildByName(ctx, f.ID, "tvshow.nfo", "movie.nfo"); err == nil {
		if err := d.readNFO(nfo, m); err != nil {
			return err
		}
		n.Title, n.Year = first(m.Title, n.Title), max(m.Year, n.Year)
		kind = map[bool]string{true: "tv", false: "movie"}[strings.EqualFold(nfo.Name, "tvshow.nfo")]
	}
	if k, _, ok := strings.Cut(m.TMDB, "/"); ok && m.Locked {
		kind = k // manually matched
	}
	if kind == "" {
		return nil
	}
	item, err := d.find(ctx, m, kind, n.Title, n.Year)
	if err != nil {
		return err
	}
	if item == nil && m.Title == "" {
		return nil // an ordinary folder, e.g. a collection
	}
	m.Kind = map[string]string{"tv": "show", "movie": "movie"}[kind]
	m.Title, m.Year = first(m.Title, n.Title), max(m.Year, n.Year)
	if item != nil {
		apply(m, item)
	}
	return nil
}

func (d folders) parentPoster(ctx context.Context, f store.File) string {
	parent, err := d.Store.Get(ctx, f.Parent)
	if err != nil {
		return ""
	}
	var pm Meta
	_ = json.Unmarshal(parent.Meta, &pm)
	return pm.Poster
}

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
	if err := xml.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&doc); err != nil {
		return nil // NFO files are often plain text links; ignore them
	}
	m.Title, m.Original, m.Summary = strings.TrimSpace(doc.Title), strings.TrimSpace(doc.Original), strings.TrimSpace(doc.Plot)
	m.Year = leadingYear(doc.Year)
	id := doc.TMDBID
	for _, u := range doc.Unique {
		if strings.EqualFold(u.Type, "tmdb") {
			id = u.Value
		}
	}
	if id = strings.TrimSpace(id); id != "" && !m.Locked {
		kind := map[bool]string{true: "tv", false: "movie"}[strings.EqualFold(nfo.Name, "tvshow.nfo")]
		m.TMDB, m.Locked = kind+"/"+id, true // an NFO id is as good as a manual match
	}
	return nil
}

// find resolves a TMDB item: a locked or known id is fetched directly,
// otherwise the best search result above the confidence threshold.
func (l *Library) find(ctx context.Context, m *Meta, kind, title string, year int) (*tmdb.Item, error) {
	if l.TMDB == nil {
		return nil, nil
	}
	if id, ok := strings.CutPrefix(m.TMDB, kind+"/"); ok {
		n, _ := strconv.Atoi(id)
		item, err := l.TMDB.Details(ctx, kind, n)
		if err == nil {
			return &item, nil
		}
		if !tmdb.IsNotFound(err) {
			return nil, transient(err)
		}
		m.TMDB, m.Locked = "", false // a stale id, e.g. from an old NFO: search instead
	}
	if title == "" {
		return nil, nil
	}
	items, err := l.TMDB.Search(ctx, kind, title, year)
	if err == nil && len(items) == 0 && year > 0 {
		items, err = l.TMDB.Search(ctx, kind, title, 0) // the year in names is often off by one
	}
	if err != nil {
		return nil, transient(err)
	}
	var best *tmdb.Item
	bestScore := .8
	for i, it := range items {
		score := matchScore(title, year, append([]string{it.Title, it.Original}, it.Aliases...), it.Year) + min(it.Popularity/1000, .05)
		if score >= bestScore {
			best, bestScore = &items[i], score
		}
	}
	if best == nil {
		return nil, nil
	}
	item, err := l.TMDB.Details(ctx, kind, best.ID)
	if err != nil {
		return nil, transient(err)
	}
	return &item, nil
}

func apply(m *Meta, it *tmdb.Item) {
	m.Title, m.Year, m.Summary, m.Rating, m.Genres = it.Title, it.Year, it.Overview, it.Rating, it.Genres
	m.TMDB = fmt.Sprintf("%s/%d", it.Type, it.ID)
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
	if errors.Is(err, tmdb.ErrUnavailable) {
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
