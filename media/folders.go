package media

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/store"
)

var discFolder = regexp.MustCompile(`(?i)^(?:cd|disc|disk)[ ._-]*[0-9]+$`)

type member struct {
	file store.File
	meta Meta
}

// Enrich only reads the index and local artwork references. Identity, title,
// synopsis and album tags come from completed file results, never an online
// directory lookup. Stable child order makes representative artwork repeatable.
//
// Movies and albums need every member to agree, so a collection never takes
// the identity of whichever file finished first. A show only needs its
// episodes and seasons to agree: unnumbered specials, promos and soundtrack
// folders are extras of the show rather than a different work.
//
// Only settled identities rise to a folder: a file whose match awaits review
// keeps that review to itself rather than lending its guess to the folder. In
// a TV library the show folder is where the shared show decision is made, so
// it carries the guess and the review; its seasons only follow.
func (d folders) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	choice := m.Match
	children, err := d.Store.Children(ctx, f.ID, 0, -1)
	if err != nil {
		return err
	}
	var films, shows, tracks, albums []member
	unknown, other := false, false
	lockedMembers := true
	members := 0
	allDiscs := true
	files, showFiles, extraFiles := 0, 0, 0
	representative := ""
	for _, child := range children {
		var cm Meta
		if len(child.Meta) > 0 {
			if err := json.Unmarshal(child.Meta, &cm); err != nil {
				return err
			}
		}
		entry := member{child, cm}
		if representative == "" && (child.Kind == "video" && !isExtra(child.Name) || child.Kind == "audio" || child.Dir && cm.Files > 0) {
			representative = memberArtwork(entry)
		}
		showMember := child.Dir && cm.Series != nil && (cm.Kind == "season" || cm.Kind == "show" || cm.Kind == "episode")
		if child.Kind == "video" && !isExtra(child.Name) || child.Kind == "audio" || showMember {
			members++
			lockedMembers = lockedMembers && cm.Locked
		}
		switch {
		case child.Dir:
			if showMember {
				shows = append(shows, entry)
				files += cm.Files
				showFiles += cm.Files
			} else if cm.Kind == "album" {
				albums = append(albums, entry)
				files += cm.Tracks
				extraFiles += cm.Tracks
				allDiscs = allDiscs && discFolder.MatchString(child.Name)
			} else if cm.Files > 0 {
				other = true
				files += cm.Files
				extraFiles += cm.Files
			}
		case child.Kind == "video" && !isExtra(child.Name):
			files++
			switch cm.Kind {
			case "movie":
				films = append(films, entry)
				extraFiles++
			case "episode":
				shows = append(shows, entry)
				showFiles++
			default:
				unknown = true
				extraFiles++
			}
		case child.Kind == "audio":
			files++
			extraFiles++
			if cm.Kind == "audio" {
				tracks = append(tracks, entry)
			} else {
				unknown = true
			}
		}
	}
	hint, base := "", ""
	if d.Hint != nil {
		hint, base = d.Hint(*f)
	}
	// Extras never outweigh the show outside a TV library, so one stray
	// episode does not turn a downloads folder into a show.
	extrasAllowed := hint == "tv" || showFiles > extraFiles
	switch {
	case len(shows) > 0 && (len(films)+len(tracks)+len(albums) == 0 && !unknown && !other || extrasAllowed):
		if sameWork(shows) && shows[0].meta.Series != nil && (hint == "tv" || settled(shows)) {
			d.show(m, f, shows, hint == "tv" && parentPath(f.Path) == base)
		}
	case unknown || other:
		// A pending/unsupported member must not silently turn a mixed
		// collection into the identity of whichever file finished first.
	case len(films) > 0 && len(shows)+len(tracks)+len(albums) == 0:
		if sameWork(films) && settled(films) {
			copyWork(m, films[0])
			m.Kind = "movie"
		}
	case len(tracks)+len(albums) > 0 && len(films)+len(shows) == 0:
		d.album(m, tracks, albums, allDiscs)
	}
	if m.Kind == "" {
		m.Match = choice // keep a persistent choice visible even while files are pending/missing
	}
	m.Locked = m.Match != nil && ((choice != nil && *choice == *m.Match) || (members > 0 && lockedMembers))
	if m.Poster == "" {
		m.Poster = representative
	}
	m.Files = files
	return d.artwork(ctx, f.ID, m)
}

// show names a folder after the series of its episodes and seasons: a season
// when its name says so, an episode when it only wraps one episode (release
// folders such as "Show.S02E06.1080p.WEB-DL"), the show otherwise.
func (d folders) show(m *Meta, f *store.File, shows []member, showFolder bool) {
	first := shows[0].meta
	season, isSeason := SeasonNumber(f.Name)
	if !showFolder && !isSeason && len(shows) == 1 && first.Kind == "episode" {
		m.Kind, m.Match, m.Series = "episode", first.Match, first.Series
		m.Title, m.Show, m.Season, m.Episode, m.Year = first.Title, first.Show, first.Season, first.Episode, first.Year
		m.Summary, m.Rating, m.Genres, m.Duration = first.Summary, first.Rating, first.Genres, first.Duration
		m.Poster, m.Backdrop = memberArtwork(shows[0]), first.Backdrop
		return
	}
	s := first.Series
	m.Match = first.Match
	m.Title, m.Original, m.Year, m.Summary = s.Title, s.Original, s.Year, s.Summary
	m.Poster, m.Backdrop, m.Rating, m.Genres = s.Poster, s.Backdrop, s.Rating, s.Genres
	m.Series = s
	if showFolder {
		m.Review = s.Review // the one place a show's doubts are reviewed
	}
	m.Kind = "show"
	if isSeason && !showFolder {
		m.Kind, m.Season = "season", season
	}
}

// settled reports whether every member's identity is decided: automatic
// without doubts, or chosen manually. A show's doubts are kept on its series.
func settled(entries []member) bool {
	for _, e := range entries {
		review := e.meta.Review
		if e.meta.Series != nil {
			review = e.meta.Series.Review
		}
		if review != "" && !e.meta.Locked {
			return false
		}
	}
	return true
}

func sameWork(entries []member) bool {
	first := entries[0].meta
	title, year := first.Title, first.Year
	if first.Series != nil {
		title, year = first.Series.Title, first.Series.Year
	}
	for _, entry := range entries {
		other := entry.meta
		if first.Match != nil && other.Match != nil {
			if *first.Match != *other.Match {
				return false
			}
			continue
		}
		ot, oy := other.Title, other.Year
		if other.Series != nil {
			ot, oy = other.Series.Title, other.Series.Year
		}
		if first.Match != nil || other.Match != nil || title == "" || normalize(title) != normalize(ot) || year != oy || localWorkKey(first) != localWorkKey(other) {
			return false
		}
	}
	return true
}

func copyWork(dst *Meta, entry member) {
	src := entry.meta
	dst.Match = src.Match
	dst.Title, dst.Original, dst.Year = src.Title, src.Original, src.Year
	dst.Summary, dst.Rating, dst.Genres = src.Summary, src.Rating, src.Genres
	dst.Poster, dst.Backdrop = memberArtwork(entry), src.Backdrop
	for _, key := range []string{"Code", "Studio", "Performers", "Runtime"} {
		dst.Set(key, src.Info[key])
	}
}

func memberArtwork(entry member) string {
	if entry.meta.Poster == "self" || entry.meta.Poster == "" && entry.file.Kind == "video" && entry.meta.Video != "" {
		return fmt.Sprintf("file:%d", entry.file.ID)
	}
	return entry.meta.Poster
}

func (d folders) album(m *Meta, tracks, albums []member, allDiscs bool) {
	entries := append(append([]member{}, tracks...), albums...)
	first := entries[0].meta
	name := first.Album
	artist := first.AlbumArtist
	if artist == "" {
		artist = first.Artist
	}
	consistent := name != "" && (len(albums) == 0 || allDiscs)
	year := first.Year
	for _, entry := range entries {
		e := entry.meta
		ea := e.AlbumArtist
		if ea == "" {
			ea = e.Artist
		}
		if normalize(e.Album) != normalize(name) || normalize(ea) != normalize(artist) {
			consistent = false
		}
		if e.Year != year {
			year = 0
		}
		m.Duration += e.Duration
		if entry.file.Dir {
			m.Tracks += e.Tracks
		} else {
			m.Tracks++
		}
		if m.Poster == "" {
			m.Poster = memberArtwork(entry)
		}
	}
	if !consistent {
		m.Albums = len(albums)
		return
	}
	m.Kind, m.Title, m.Album, m.Artist, m.AlbumArtist, m.Year = "album", name, name, artist, artist, year
}

func (l *Library) artwork(ctx context.Context, id int64, m *Meta) error {
	for _, art := range []struct {
		names []string
		dest  *string
	}{{posterNames, &m.Poster}, {backdropNames, &m.Backdrop}} {
		f, err := l.Store.ChildByName(ctx, id, art.names...)
		if err == nil {
			*art.dest = fmt.Sprintf("file:%d", f.ID)
		} else if err != store.ErrNotFound {
			return err
		}
	}
	var versions []string
	for _, ref := range []string{m.Poster, m.Backdrop} {
		if id, ok := strings.CutPrefix(ref, "file:"); ok {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				return err
			}
			f, err := l.Store.Get(ctx, n)
			if err != nil {
				if err == store.ErrNotFound {
					versions = append(versions, "missing")
					continue
				}
				return err
			}
			versions = append(versions, strconv.FormatInt(f.Revision, 10))
		} else {
			versions = append(versions, "")
		}
	}
	if versions[0] != "" || versions[1] != "" {
		m.ArtworkVersion = strings.Join(versions, ":")
	}
	return nil
}

// Prefer a code to a title when grouping JAV results without an online match.
func localWorkKey(m Meta) string {
	return strings.ToLower(m.Info["Code"])
}
