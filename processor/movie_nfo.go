package processor

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

var errUnsupportedNFO = errors.New("unsupported NFO document")

var sidecarNames = []string{
	"folder.jpg", "folder.jpeg", "folder.png", "poster.jpg", "poster.jpeg", "poster.png",
	"cover.jpg", "cover.jpeg", "cover.png", "backdrop.jpg", "backdrop.jpeg", "backdrop.png",
	"fanart.jpg", "fanart.jpeg", "fanart.png", "background.jpg", "background.jpeg", "background.png",
}

func isFolderArtwork(name string) bool { return folderArtworkKind(name) != "" }
func folderArtworkKind(name string) string {
	value := strings.ToLower(name)
	for _, candidate := range sidecarNames {
		if value == candidate {
			if strings.HasPrefix(value, "backdrop.") || strings.HasPrefix(value, "fanart.") || strings.HasPrefix(value, "background.") {
				return "backdrop"
			}
			return "primary"
		}
	}
	ext := filepath.Ext(value)
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		return ""
	}
	stem := strings.TrimSuffix(value, ext)
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

func companionArtwork(entries []model.Entry, nfo model.Entry, kind string) (model.Entry, bool) {
	nfoStem := strings.TrimSuffix(strings.ToLower(nfo.Name), filepath.Ext(nfo.Name))
	for _, entry := range entries {
		if folderArtworkKind(entry.Name) != kind {
			continue
		}
		if nfoStem == "" {
			return entry, true
		}
		stem := strings.TrimSuffix(strings.ToLower(entry.Name), filepath.Ext(entry.Name))
		for _, suffix := range []string{"-poster", "-cover", "-backdrop", "-fanart", "-background"} {
			if stem == nfoStem+suffix {
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
	Plot          string      `xml:"plot"`
	Year          string      `xml:"year"`
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

type nfoReader struct{ storages *storage.Registry }

func (p nfoReader) read(ctx context.Context, entry model.Entry) (nfoDocument, error) {
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
	if err := xml.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&document); err != nil {
		return nfoDocument{}, fmt.Errorf("parse %s: %w", filepath.Base(entry.Path), err)
	}
	if document.XMLName.Local != "tvshow" && document.XMLName.Local != "movie" {
		return nfoDocument{}, fmt.Errorf("%w: root %q", errUnsupportedNFO, document.XMLName.Local)
	}
	return document, nil
}

type parsedDirectoryNFO struct {
	entry    model.Entry
	document nfoDocument
}

func (p nfoReader) selectDirectoryNFO(ctx context.Context, directory model.Entry, explicit, candidates []model.Entry) (model.Entry, *nfoDocument, bool, error) {
	read := func(entries []model.Entry) ([]parsedDirectoryNFO, error) {
		result := make([]parsedDirectoryNFO, 0, len(entries))
		for _, entry := range entries {
			document, err := p.read(ctx, entry)
			if errors.Is(err, errUnsupportedNFO) {
				continue
			}
			if err != nil {
				return nil, err
			}
			result = append(result, parsedDirectoryNFO{entry, document})
		}
		return result, nil
	}
	if parsed, err := read(explicit); err != nil {
		return model.Entry{}, nil, false, err
	} else if len(parsed) > 0 {
		return parsed[0].entry, &parsed[0].document, false, nil
	}
	parsed, err := read(candidates)
	if err != nil || len(parsed) == 0 {
		return model.Entry{}, nil, false, err
	}
	if len(parsed) == 1 {
		return parsed[0].entry, &parsed[0].document, false, nil
	}
	identities := map[string]bool{}
	for _, candidate := range parsed {
		identities[nfoDocumentIdentity(candidate.document)] = true
	}
	if len(identities) == 1 {
		return parsed[0].entry, &parsed[0].document, false, nil
	}
	directoryTitle := ParseMovieName(directory.Name).Title
	bestIndex, bestScore, secondScore := -1, float64(0), float64(0)
	for index, candidate := range parsed {
		filenameTitle := ParseMovieName(candidate.entry.Name).Title
		score := math.Max(titleScore(directoryTitle, candidate.document.Title), titleScore(filenameTitle, candidate.document.Title))
		if score > bestScore {
			secondScore, bestScore, bestIndex = bestScore, score, index
		} else if score > secondScore {
			secondScore = score
		}
	}
	if bestIndex >= 0 && bestScore >= .8 && bestScore-secondScore >= .1 {
		return parsed[bestIndex].entry, &parsed[bestIndex].document, false, nil
	}
	return model.Entry{}, nil, true, nil
}

func nfoDocumentIdentity(document nfoDocument) string {
	for _, provider := range []string{"tmdb", "imdb", "tvdb"} {
		if value := document.providerIDs()[provider]; value != "" {
			return provider + ":" + value
		}
	}
	return document.XMLName.Local + ":" + normalizedTitle(document.Title) + ":" + strings.TrimSpace(document.Year)
}
