package media

import (
	"context"
	"encoding/json"
	"errors"
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
	case "mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "wma", "aiff", "ape", "jpg", "jpeg", "png", "gif", "epub", "pdf":
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
