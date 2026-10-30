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
	files := 0
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
		if child.Kind == "video" && !isExtra(child.Name) || child.Kind == "audio" || child.Dir && cm.Kind == "season" {
			members++
			lockedMembers = lockedMembers && cm.Locked
		}
		switch {
		case child.Dir:
			if cm.Kind == "season" {
				shows = append(shows, entry)
				files += cm.Files
			} else if cm.Kind == "album" {
				albums = append(albums, entry)
				files += cm.Tracks
				allDiscs = allDiscs && discFolder.MatchString(child.Name)
			} else if cm.Files > 0 {
				other = true
				files += cm.Files
			}
		case child.Kind == "video" && !isExtra(child.Name):
			files++
			switch cm.Kind {
			case "movie":
				films = append(films, entry)
			case "episode":
				shows = append(shows, entry)
			default:
				unknown = true
			}
		case child.Kind == "audio":
			files++
			if cm.Kind == "audio" {
				tracks = append(tracks, entry)
			} else {
				unknown = true
			}
		}
	}
	// A pending/unsupported member must not silently turn a mixed collection
	// into the identity of whichever file happened to finish first.
	if !unknown && !other {
		switch {
		case len(films) > 0 && len(shows)+len(tracks)+len(albums) == 0:
			if sameWork(films) {
				copyWork(m, films[0])
				m.Kind = "movie"
			}
		case len(shows) > 0 && len(films)+len(tracks)+len(albums) == 0:
			if sameWork(shows) && shows[0].meta.Series != nil {
				s := shows[0].meta.Series
				m.Match = shows[0].meta.Match
				m.Title, m.Original, m.Year, m.Summary = s.Title, s.Original, s.Year, s.Summary
				m.Poster, m.Backdrop, m.Rating, m.Genres = s.Poster, s.Backdrop, s.Rating, s.Genres
				m.Review, m.Series = s.Review, s
				m.Kind = "show"
				if season, ok := SeasonNumber(f.Name); ok {
					m.Kind, m.Season = "season", season
				}
			}
		case len(tracks)+len(albums) > 0 && len(films)+len(shows) == 0:
			d.album(m, tracks, albums, allDiscs)
		}
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
	dst.Review = src.Review
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
