package media

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

var sidecarNames = []string{
	"tvshow.nfo", "movie.nfo",
	"folder.jpg", "folder.jpeg", "folder.png",
	"poster.jpg", "poster.jpeg", "poster.png",
	"cover.jpg", "cover.jpeg", "cover.png",
	"backdrop.jpg", "backdrop.jpeg", "backdrop.png",
	"fanart.jpg", "fanart.jpeg", "fanart.png",
	"background.jpg", "background.jpeg", "background.png",
}

var errUnsupportedNFO = errors.New("unsupported NFO document")

type Sidecar struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
}

func NewSidecar(catalog *catalog.Catalog, storages *storage.Registry) *Sidecar {
	return &Sidecar{catalog: catalog, storages: storages}
}

func (p *Sidecar) Name() string { return "media_sidecar" }

func (p *Sidecar) Match(entry model.Entry) bool {
	if entry.Type != model.EntryFile || strings.HasPrefix(entry.Name, "._") {
		return false
	}
	if isFolderArtwork(entry.Name) || strings.EqualFold(entry.Extension, "nfo") {
		return true
	}
	// Reconcile the video's direct folder after Cataloger and Matcher have run
	// in the same pipeline. This makes artwork-only folders independent of job
	// ordering: folder.jpg may be processed before or after the video.
	switch strings.ToLower(entry.Extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv", "rmvb":
		return true
	default:
		return false
	}
}

func isFolderArtwork(name string) bool {
	return folderArtworkKind(name) != ""
}

func folderArtworkKind(name string) string {
	value := strings.ToLower(name)
	for _, candidate := range sidecarNames[2:] {
		if value == candidate {
			if strings.HasPrefix(candidate, "backdrop.") || strings.HasPrefix(candidate, "fanart.") || strings.HasPrefix(candidate, "background.") {
				return "backdrop"
			}
			return "primary"
		}
	}
	extension := filepath.Ext(value)
	if extension != ".jpg" && extension != ".jpeg" && extension != ".png" {
		return ""
	}
	stem := strings.TrimSuffix(value, extension)
	for _, suffix := range []string{"-poster", "-cover"} {
		if strings.HasSuffix(stem, suffix) {
			return "primary"
		}
	}
	for _, suffix := range []string{"-backdrop", "-fanart", "-background"} {
		if strings.HasSuffix(stem, suffix) {
			return "backdrop"
		}
	}
	return ""
}

func (p *Sidecar) Process(ctx context.Context, entry model.Entry) error {
	if isFolderArtwork(entry.Name) {
		if err := p.catalog.RemoveMediaFileRole(ctx, entry.ID, "photo"); err != nil {
			return err
		}
	}
	if entry.ParentID == nil {
		return nil
	}
	directory, err := p.catalog.Entry(ctx, *entry.ParentID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.applyDirectory(ctx, *directory)
}

func (p *Sidecar) applyDirectory(ctx context.Context, directory model.Entry) error {
	children, err := p.catalog.DirectoryMediaSidecars(ctx, directory.ID)
	if err != nil {
		return err
	}
	byName := make(map[string]model.Entry, len(children))
	for _, child := range children {
		byName[strings.ToLower(child.Name)] = child
	}
	var nfoEntry model.Entry
	var document *nfoDocument
	nfoCandidates := make([]model.Entry, 0)
	for _, name := range []string{"tvshow.nfo", "movie.nfo"} {
		if candidate, ok := byName[name]; ok {
			nfoCandidates = append(nfoCandidates, candidate)
		}
	}
	for _, candidate := range children {
		if !strings.EqualFold(candidate.Extension, "nfo") || strings.EqualFold(candidate.Name, "tvshow.nfo") || strings.EqualFold(candidate.Name, "movie.nfo") {
			continue
		}
		nfoCandidates = append(nfoCandidates, candidate)
	}
	for _, candidate := range nfoCandidates {
		value, readErr := p.readNFO(ctx, candidate)
		if readErr != nil {
			if errors.Is(readErr, errUnsupportedNFO) {
				continue
			}
			return readErr
		}
		nfoEntry = candidate
		document = &value
		break
	}
	var item *model.MediaItem
	if document != nil {
		expected := "movie"
		if document.XMLName.Local == "tvshow" {
			expected = "series"
		}
		externalID := "nfo:" + directory.StorageID + ":" + directory.Path
		if tmdbID := document.providerIDs()["tmdb"]; tmdbID != "" {
			externalID = "tmdb:" + tmdbID
			item, err = p.catalog.MediaItemByExternalID(ctx, expected, externalID)
		}
		if item == nil && (err == nil || errors.Is(err, catalog.ErrNotFound)) {
			title := strings.TrimSpace(document.Title)
			if title == "" {
				title = directory.Name
			}
			item, err = p.catalog.UpsertMediaItem(ctx, model.MediaItem{Type: expected, Title: title,
				SortTitle: sortTitle(title), ExternalID: externalID, MatchSource: "nfo", MatchConfidence: 1})
			if err != nil {
				item, err = p.catalog.MediaItemByExternalID(ctx, expected, externalID)
			}
		}
	}
	if document == nil && item == nil && (err == nil || errors.Is(err, catalog.ErrNotFound)) {
		item, err = p.catalog.MediaItemForEntry(ctx, directory.ID, "folder")
		if errors.Is(err, catalog.ErrNotFound) {
			item, err = p.catalog.DirectMovieForDirectory(ctx, directory)
		}
	}
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	metadata := map[string]any{}
	_ = json.Unmarshal(item.Metadata, &metadata)
	if document != nil {
		expected := "movie"
		if document.XMLName.Local == "tvshow" {
			expected = "series"
		}
		if item.Type != expected {
			return nil
		}
		applyNFO(item, metadata, *document, nfoEntry.ID)
		if err := p.catalog.AssociateMediaFile(ctx, item.ID, nfoEntry.ID, "metadata"); err != nil {
			return err
		}
	}
	poster, hasPoster := firstNamed(byName, "folder.jpg", "folder.jpeg", "folder.png", "poster.jpg", "poster.jpeg", "poster.png", "cover.jpg", "cover.jpeg", "cover.png")
	if !hasPoster {
		poster, hasPoster = companionArtwork(children, nfoEntry, "primary")
	}
	if hasPoster {
		metadata["localPosterEntryId"] = poster.ID
		if err := p.catalog.AssociateMediaFile(ctx, item.ID, poster.ID, "artwork-primary"); err != nil {
			return err
		}
	}
	backdrop, hasBackdrop := firstNamed(byName, "backdrop.jpg", "backdrop.jpeg", "backdrop.png", "fanart.jpg", "fanart.jpeg", "fanart.png", "background.jpg", "background.jpeg", "background.png")
	if !hasBackdrop {
		backdrop, hasBackdrop = companionArtwork(children, nfoEntry, "backdrop")
	}
	if hasBackdrop {
		metadata["localBackdropEntryId"] = backdrop.ID
		if err := p.catalog.AssociateMediaFile(ctx, item.ID, backdrop.ID, "artwork-backdrop"); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	item.Metadata = encoded
	if _, err := p.catalog.UpsertMediaItem(ctx, *item); err != nil {
		return err
	}
	if err := p.catalog.RemoveMediaFileRole(ctx, directory.ID, "folder"); err != nil {
		return err
	}
	return p.catalog.AssociateMediaFile(ctx, item.ID, directory.ID, "folder")
}

func companionArtwork(entries []model.Entry, nfo model.Entry, kind string) (model.Entry, bool) {
	nfoStem := strings.TrimSuffix(strings.ToLower(nfo.Name), filepath.Ext(nfo.Name))
	for _, entry := range entries {
		if folderArtworkKind(entry.Name) != kind {
			continue
		}
		if nfoStem == "" {
			return entry, true
		}
		imageStem := strings.TrimSuffix(strings.ToLower(entry.Name), filepath.Ext(entry.Name))
		for _, suffix := range []string{"-poster", "-cover", "-backdrop", "-fanart", "-background"} {
			if imageStem == nfoStem+suffix {
				return entry, true
			}
		}
	}
	return model.Entry{}, false
}

func firstNamed(entries map[string]model.Entry, names ...string) (model.Entry, bool) {
	for _, name := range names {
		if entry, ok := entries[name]; ok {
			return entry, true
		}
	}
	return model.Entry{}, false
}

type nfoDocument struct {
	XMLName       xml.Name
	Title         string      `xml:"title"`
	OriginalTitle string      `xml:"originaltitle"`
	SortTitle     string      `xml:"sorttitle"`
	Plot          string      `xml:"plot"`
	Outline       string      `xml:"outline"`
	Year          string      `xml:"year"`
	Premiered     string      `xml:"premiered"`
	Rating        string      `xml:"rating"`
	Genres        []string    `xml:"genre"`
	Studios       []string    `xml:"studio"`
	TMDBID        string      `xml:"tmdbid"`
	TVDBID        string      `xml:"tvdbid"`
	IMDbID        string      `xml:"imdbid"`
	UniqueIDs     []nfoUnique `xml:"uniqueid"`
}

type nfoUnique struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func (document nfoDocument) providerIDs() map[string]string {
	result := map[string]string{}
	for key, value := range map[string]string{"tmdb": document.TMDBID, "tvdb": document.TVDBID, "imdb": document.IMDbID} {
		if value = strings.TrimSpace(value); value != "" {
			result[key] = value
		}
	}
	for _, unique := range document.UniqueIDs {
		if key, value := strings.ToLower(strings.TrimSpace(unique.Type)), strings.TrimSpace(unique.Value); key != "" && value != "" {
			result[key] = value
		}
	}
	return result
}

func (p *Sidecar) readNFO(ctx context.Context, entry model.Entry) (nfoDocument, error) {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return nfoDocument{}, storage.ErrOffline
	}
	file, err := backend.Open(ctx, entry.Path)
	if err != nil {
		return nfoDocument{}, err
	}
	defer file.Close()
	var document nfoDocument
	decoder := xml.NewDecoder(io.LimitReader(file, 4<<20))
	if err := decoder.Decode(&document); err != nil {
		return nfoDocument{}, fmt.Errorf("parse %s: %w", filepath.Base(entry.Path), err)
	}
	if document.XMLName.Local != "tvshow" && document.XMLName.Local != "movie" {
		return nfoDocument{}, fmt.Errorf("%w: root %q", errUnsupportedNFO, document.XMLName.Local)
	}
	return document, nil
}

func applyNFO(item *model.MediaItem, metadata map[string]any, document nfoDocument, entryID string) {
	if value := strings.TrimSpace(document.Title); value != "" {
		item.Title = value
	}
	if value := strings.TrimSpace(document.SortTitle); value != "" {
		item.SortTitle = value
	} else if item.Title != "" {
		item.SortTitle = sortTitle(item.Title)
	}
	if len(document.Year) >= 4 {
		if value, err := strconv.Atoi(document.Year[:4]); err == nil {
			item.Year = &value
		}
	}
	if value := strings.TrimSpace(document.Plot); value != "" {
		metadata["overview"] = value
	} else if value := strings.TrimSpace(document.Outline); value != "" {
		metadata["overview"] = value
	}
	for key, value := range map[string]string{
		"originalTitle": document.OriginalTitle,
		"premiered":     document.Premiered,
		"rating":        document.Rating,
	} {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	if values := cleanNFOValues(document.Genres); len(values) > 0 {
		metadata["genres"] = values
	}
	if values := cleanNFOValues(document.Studios); len(values) > 0 {
		metadata["studios"] = values
	}
	providerIDs := document.providerIDs()
	if len(providerIDs) > 0 {
		metadata["providerIds"] = providerIDs
	}
	metadata["nfoEntryId"] = entryID
	metadata["metadataSource"] = "nfo"
	item.MatchSource = "nfo"
	item.MatchConfidence = 1
}

func cleanNFOValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
