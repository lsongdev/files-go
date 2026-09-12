package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

type Cataloger struct{ catalog *catalog.Catalog }

func NewCataloger(catalog *catalog.Catalog) *Cataloger { return &Cataloger{catalog: catalog} }
func (p *Cataloger) Name() string                      { return "media_catalog" }
func (p *Cataloger) Match(entry model.Entry) bool {
	if entry.Type != model.EntryFile || strings.HasPrefix(entry.Name, "._") || strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "wma", "aiff", "ape", "jpg", "jpeg", "png", "gif", "epub", "pdf",
		"mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv":
		return true
	default:
		return false
	}
}

func (p *Cataloger) Process(ctx context.Context, entry model.Entry) error {
	technical, err := p.catalog.MediaFile(ctx, entry.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if technical.Kind == "audio" {
		return p.catalogAudio(ctx, entry, *technical)
	}
	itemType, role := "", ""
	switch technical.Kind {
	case "photo":
		itemType, role = "photo", "photo"
	case "book":
		itemType, role = "book", "book"
	case "video":
		suppressed, err := p.catalog.MediaMatchSuppressed(ctx, entry.ID)
		if err != nil || suppressed {
			return err
		}
		return p.catalogVideo(ctx, entry, *technical)
	default:
		return nil
	}
	if _, err := p.catalog.MediaItemForEntry(ctx, entry.ID, role); err == nil {
		return nil
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	title := strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name))
	metadata := technical.Metadata
	if itemType == "track" {
		var document struct {
			Music struct {
				Title  string `json:"title"`
				Artist string `json:"artist"`
				Album  string `json:"album"`
			} `json:"music"`
		}
		if json.Unmarshal(technical.Metadata, &document) == nil && document.Music.Title != "" {
			title = document.Music.Title
		}
	}
	if itemType == "book" {
		var document struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(technical.Metadata, &document) == nil && document.Title != "" {
			title = document.Title
		}
	}
	externalID := "entry:" + entry.ID
	item, err := p.catalog.MediaItemByExternalID(ctx, itemType, externalID)
	if errors.Is(err, catalog.ErrNotFound) {
		item, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: itemType, Title: title, SortTitle: sortTitle(title), ExternalID: externalID, MatchSource: "embedded", MatchConfidence: 1, Metadata: metadata})
	}
	if err != nil {
		return err
	}
	return p.catalog.AssociateMediaFile(ctx, item.ID, entry.ID, role)
}

func (p *Cataloger) catalogAudio(ctx context.Context, entry model.Entry, technical model.MediaFile) error {
	var document struct {
		Music struct {
			Title       string `json:"title"`
			Artist      string `json:"artist"`
			AlbumArtist string `json:"album_artist"`
			Album       string `json:"album"`
			Track       string `json:"track"`
		} `json:"music"`
	}
	_ = json.Unmarshal(technical.Metadata, &document)
	title := strings.TrimSpace(document.Music.Title)
	if title == "" {
		title = strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name))
	}
	artistName := strings.TrimSpace(document.Music.AlbumArtist)
	if artistName == "" {
		artistName = strings.TrimSpace(document.Music.Artist)
	}
	albumName := strings.TrimSpace(document.Music.Album)

	parentID := ""
	var artist, album *model.MediaItem
	var err error
	if artistName != "" {
		artist, err = p.ensureEmbeddedMediaItem(ctx, "artist", artistName, "embedded:artist:"+normalizedTitle(artistName), "", nil, nil)
		if err != nil {
			return err
		}
		parentID = artist.ID
	}
	if albumName != "" {
		albumKey := "embedded:album:" + normalizedTitle(artistName) + ":" + normalizedTitle(albumName)
		album, err = p.ensureEmbeddedMediaItem(ctx, "album", albumName, albumKey, parentID, nil, technical.Metadata)
		if err != nil {
			return err
		}
		parentID = album.ID
	}
	trackNumber := leadingNumber(document.Music.Track)
	track, err := p.catalog.MediaItemForEntry(ctx, entry.ID, "audio")
	if errors.Is(err, catalog.ErrNotFound) {
		track, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "track", Title: title, SortTitle: sortTitle(title), ParentID: parentID,
			IndexNumber: trackNumber, ExternalID: "entry:" + entry.ID, MatchSource: "embedded", MatchConfidence: 1, Metadata: technical.Metadata})
	} else if err == nil && (track.ParentID != parentID || track.IndexNumber == nil && trackNumber != nil) {
		track.ParentID = parentID
		track.IndexNumber = trackNumber
		track.Metadata = technical.Metadata
		track, err = p.catalog.UpsertMediaItem(ctx, *track)
	}
	if err != nil {
		return err
	}
	for _, association := range []struct {
		item *model.MediaItem
		role string
	}{{artist, "artist"}, {album, "album"}, {track, "audio"}} {
		if association.item != nil {
			if err := p.catalog.AssociateMediaFile(ctx, association.item.ID, entry.ID, association.role); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Cataloger) ensureEmbeddedMediaItem(ctx context.Context, itemType, title, externalID, parentID string, indexNumber *int, metadata []byte) (*model.MediaItem, error) {
	item, err := p.catalog.MediaItemByExternalID(ctx, itemType, externalID)
	if errors.Is(err, catalog.ErrNotFound) {
		item, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: itemType, Title: title, SortTitle: sortTitle(title), ParentID: parentID,
			IndexNumber: indexNumber, ExternalID: externalID, MatchSource: "embedded", MatchConfidence: 1, Metadata: metadata})
	}
	return item, err
}

func leadingNumber(value string) *int {
	value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	if value == "" {
		return nil
	}
	number := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			break
		}
		number = number*10 + int(char-'0')
	}
	if number <= 0 {
		return nil
	}
	return &number
}

func (p *Cataloger) catalogVideo(ctx context.Context, entry model.Entry, technical model.MediaFile) error {
	libraryTypes, err := p.catalog.LibraryTypesForEntry(ctx, entry)
	if err != nil {
		return err
	}
	parsed := ParseName(entry.Name)
	if parsed.Title == "" {
		parsed.Title = strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name))
	}
	if contains(libraryTypes, "tv") && parsed.Season != nil && parsed.Episode != nil {
		return p.catalogEpisode(ctx, entry, technical, parsed)
	}
	if !contains(libraryTypes, "movies") {
		return nil
	}
	externalID := "entry:" + entry.ID
	item, err := p.catalog.MediaItemByExternalID(ctx, "movie", externalID)
	if errors.Is(err, catalog.ErrNotFound) {
		item, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: parsed.Title, SortTitle: sortTitle(parsed.Title), Year: parsed.Year,
			ExternalID: externalID, MatchSource: "filename", MatchConfidence: .6, Metadata: technical.Metadata})
	}
	if err != nil {
		return err
	}
	return p.catalog.AssociateMediaFile(ctx, item.ID, entry.ID, "video")
}

func (p *Cataloger) catalogEpisode(ctx context.Context, entry model.Entry, technical model.MediaFile, parsed ParsedName) error {
	seriesID := "filename:" + normalizedTitle(parsed.Title)
	series, err := p.catalog.MediaItemByExternalID(ctx, "series", seriesID)
	if errors.Is(err, catalog.ErrNotFound) {
		series, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "series", Title: parsed.Title, SortTitle: sortTitle(parsed.Title), Year: parsed.Year,
			ExternalID: seriesID, MatchSource: "filename", MatchConfidence: .6})
	}
	if err != nil {
		return err
	}
	season, err := p.catalog.MediaChild(ctx, series.ID, "season", *parsed.Season)
	if errors.Is(err, catalog.ErrNotFound) {
		title := fmt.Sprintf("%s — Season %d", series.Title, *parsed.Season)
		season, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "season", Title: title, SortTitle: fmt.Sprintf("%04d", *parsed.Season), ParentID: series.ID,
			IndexNumber: parsed.Season, MatchSource: "filename", MatchConfidence: .6})
	}
	if err != nil {
		return err
	}
	episode, err := p.catalog.MediaChild(ctx, season.ID, "episode", *parsed.Episode)
	if errors.Is(err, catalog.ErrNotFound) {
		title := fmt.Sprintf("Episode %d", *parsed.Episode)
		episode, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: "episode", Title: title, SortTitle: fmt.Sprintf("%04d", *parsed.Episode), ParentID: season.ID,
			IndexNumber: parsed.Episode, MatchSource: "filename", MatchConfidence: .6, Metadata: technical.Metadata})
	}
	if err != nil {
		return err
	}
	for _, association := range []struct{ id, role string }{{series.ID, "series"}, {season.ID, "season"}, {episode.ID, "video"}} {
		if err := p.catalog.AssociateMediaFile(ctx, association.id, entry.ID, association.role); err != nil {
			return err
		}
	}
	return nil
}
