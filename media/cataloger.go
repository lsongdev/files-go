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
	if entry.Type != model.EntryFile || strings.HasPrefix(entry.Name, "._") {
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
	itemType, role := "", ""
	switch technical.Kind {
	case "audio":
		itemType, role = "track", "audio"
	case "photo":
		itemType, role = "photo", "photo"
	case "book":
		itemType, role = "book", "book"
	case "video":
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
