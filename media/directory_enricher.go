package media

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

// DirectoryEnricher assigns local sidecars to the directory itself. It does
// not copy a child's movie identity to the parent directory.
type DirectoryEnricher struct {
	catalog  *catalog.Catalog
	nfo      nfoReader
	artwork  *Artwork
	provider MetadataProvider
	language string
}

func NewDirectoryEnricher(catalog *catalog.Catalog, storages *storage.Registry) *DirectoryEnricher {
	return NewDirectoryEnricherWithProvider(catalog, storages, nil, "")
}

func NewDirectoryEnricherWithProvider(catalog *catalog.Catalog, storages *storage.Registry, provider MetadataProvider, language string) *DirectoryEnricher {
	if language == "" {
		language = "zh-CN"
	}
	return &DirectoryEnricher{catalog: catalog, nfo: nfoReader{storages: storages}, provider: provider, language: language}
}

func (p *DirectoryEnricher) SetArtwork(artwork *Artwork) { p.artwork = artwork }

func (p *DirectoryEnricher) Name() string { return "directory_enrichment" }
func (p *DirectoryEnricher) Match(entry model.Entry) bool {
	if entry.Type != model.EntryFile || strings.HasPrefix(entry.Name, "._") || strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	return isFolderArtwork(entry.Name) || strings.EqualFold(entry.Extension, "nfo") || movieVideoExtension(entry.Extension)
}

func (p *DirectoryEnricher) Process(ctx context.Context, entry model.Entry) error {
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
	if err := p.ProcessDirectory(ctx, *directory); err != nil {
		return err
	}
	if seasonDir.MatchString(directory.Name) && directory.ParentID != nil {
		show, err := p.catalog.Entry(ctx, *directory.ParentID)
		if err != nil {
			return err
		}
		return p.ProcessDirectory(ctx, *show)
	}
	return nil
}

func (p *DirectoryEnricher) ProcessDirectory(ctx context.Context, directory model.Entry) error {
	if directory.Type != model.EntryDirectory || !directory.Available {
		return nil
	}
	children, err := p.catalog.DirectoryMediaSidecars(ctx, directory.ID)
	if err != nil {
		return err
	}
	byName := make(map[string]model.Entry, len(children))
	var explicit, candidates []model.Entry
	for _, child := range children {
		byName[strings.ToLower(child.Name)] = child
		if strings.EqualFold(child.Extension, "nfo") {
			if strings.EqualFold(child.Name, "movie.nfo") || strings.EqualFold(child.Name, "tvshow.nfo") {
				explicit = append(explicit, child)
			} else {
				candidates = append(candidates, child)
			}
		}
	}
	var artwork catalog.MediaCandidate
	if entry, ok := firstNamed(byName, "folder.jpg", "folder.jpeg", "folder.png", "poster.jpg", "poster.jpeg", "poster.png", "cover.jpg", "cover.jpeg", "cover.png"); ok {
		artwork.Icon = "file:" + entry.ID
	} else if entry, ok := companionArtwork(children, model.Entry{}, "primary"); ok {
		artwork.Icon = "file:" + entry.ID
	}
	if entry, ok := firstNamed(byName, "backdrop.jpg", "backdrop.jpeg", "backdrop.png", "fanart.jpg", "fanart.jpeg", "fanart.png", "background.jpg", "background.jpeg", "background.png"); ok {
		artwork.Backdrop = "file:" + entry.ID
	} else if entry, ok := companionArtwork(children, model.Entry{}, "backdrop"); ok {
		artwork.Backdrop = "file:" + entry.ID
	}
	if artwork.Icon == "" && artwork.Backdrop == "" {
		if _, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "local_artwork"); err != nil {
			return err
		}
	} else if _, err := p.catalog.SetMediaCandidate(ctx, directory.ID, "local_artwork", artwork); err != nil {
		return err
	}
	nfoEntry, document, ambiguous, err := p.nfo.selectDirectoryNFO(ctx, directory, explicit, candidates)
	if err != nil {
		return err
	}
	if ambiguous || document == nil {
		if _, err = p.catalog.ClearMediaCandidate(ctx, directory.ID, "local_nfo"); err != nil {
			return err
		}
		if ambiguous {
			_, err = p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
			return err
		}
		return p.enrichInferredDirectory(ctx, directory)
	}
	kind := "movie"
	if document.XMLName.Local == "tvshow" {
		kind = "tv"
	}
	nfo := catalog.MediaCandidate{Kind: kind, Title: strings.TrimSpace(document.Title), Summary: strings.TrimSpace(document.Plot)}
	if len(document.Year) >= 4 {
		if year, err := strconv.Atoi(document.Year[:4]); err == nil {
			nfo.Year = &year
		}
	}
	nfo.Data, err = json.Marshal(map[string]any{
		"entryId": nfoEntry.ID, "overview": strings.TrimSpace(document.Plot),
		"originalTitle": strings.TrimSpace(document.OriginalTitle), "providerIds": document.providerIDs(),
	})
	if err != nil {
		return err
	}
	_, err = p.catalog.SetMediaCandidate(ctx, directory.ID, "local_nfo", nfo)
	return err
}

func (p *DirectoryEnricher) enrichInferredDirectory(ctx context.Context, directory model.Entry) error {
	types, err := p.catalog.LibraryTypesForEntry(ctx, directory)
	if err != nil {
		return err
	}
	if contains(types, "tv") {
		return p.enrichTVDirectory(ctx, directory)
	}
	return p.enrichMovieDirectory(ctx, directory)
}

func (p *DirectoryEnricher) enrichTVDirectory(ctx context.Context, directory model.Entry) error {
	if p.provider == nil {
		return nil
	}
	root, err := p.catalog.IsLibrarySourceRoot(ctx, directory)
	if err != nil {
		return err
	}
	if root || seasonDir.MatchString(directory.Name) {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	parsed := ParseName(directory.Name)
	// Directory names are not release filenames. In particular, a show title
	// ending in "Saul" must not be interpreted as an Sxx season token.
	parsed.Title = strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(directory.Name))
	if parsed.Year != nil {
		parsed.Title = strings.TrimSpace(strings.Replace(parsed.Title, strconv.Itoa(*parsed.Year), "", 1))
	}
	if parsed.Title == "" {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	children, err := p.catalog.Children(ctx, directory.ID, catalog.ListOptions{Limit: 500})
	if err != nil {
		return err
	}
	if len(children) >= 500 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	episodes := 0
	for _, child := range children {
		if child.Type == model.EntryDirectory && seasonDir.MatchString(child.Name) {
			files, err := p.catalog.Children(ctx, child.ID, catalog.ListOptions{Limit: 500})
			if err != nil {
				return err
			}
			if len(files) >= 500 {
				_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
				return err
			}
			for _, file := range files {
				if file.Type == model.EntryFile && movieVideoExtension(file.Extension) && !isAuxiliaryVideo(file.Name) {
					if !episodeBelongsToSeries(file, parsed.Title) {
						_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
						return err
					}
					episodes++
				}
			}
		} else if child.Type == model.EntryFile && movieVideoExtension(child.Extension) && !isAuxiliaryVideo(child.Name) {
			if !episodeBelongsToSeries(child, parsed.Title) {
				_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
				return err
			}
			episodes++
		}
	}
	if episodes == 0 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	if current, err := p.catalog.MediaForEntry(ctx, directory.ID); err == nil {
		if current.MatchLocked {
			return nil
		}
		var sources map[string]catalog.MediaCandidate
		if json.Unmarshal(current.Sources, &sources) == nil {
			if previous, ok := sources["tmdb"]; ok && previous.Kind == "tv" {
				var candidate Candidate
				if json.Unmarshal(previous.Data, &candidate) == nil {
					if _, score, matched := bestCandidate(parsed, []Candidate{candidate}); matched && score >= .8 {
						if p.artwork != nil {
							return p.artwork.ProcessEntry(ctx, directory.ID)
						}
						return nil
					}
				}
			}
		}
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	results, err := p.provider.Search(ctx, Query{Type: "tv", Title: parsed.Title, Year: parsed.Year, Language: p.language})
	if err != nil {
		return err
	}
	match, score, ok := bestCandidate(parsed, results)
	if !ok || score < .8 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	if details, err := p.provider.Fetch(ctx, "tv", match.ID, p.language); err == nil {
		match = details
	}
	encoded, err := json.Marshal(match)
	if err != nil {
		return err
	}
	if _, err := p.catalog.SetMediaCandidate(ctx, directory.ID, "tmdb", catalog.MediaCandidate{
		Kind: "tv", Title: match.Title, Year: match.Year, Summary: strings.TrimSpace(match.Overview), Data: encoded,
	}); err != nil {
		return err
	}
	if p.artwork != nil {
		return p.artwork.ProcessEntry(ctx, directory.ID)
	}
	return nil
}

func episodeBelongsToSeries(entry model.Entry, title string) bool {
	parsed := ParsedNameForEntry(entry, true)
	if parsed.Season == nil || parsed.Episode == nil {
		return false
	}
	showName, episodeName := normalizedTitle(title), normalizedTitle(parsed.Title)
	return showName != "" && (showName == episodeName || len([]rune(showName)) >= 6 && strings.Contains(episodeName, showName))
}

// A matching filename alone is insufficient to assign a movie identity to a
// directory. The directory name and every primary video must agree with the
// same provider candidate; collections and library roots remain unenhanced.
func (p *DirectoryEnricher) enrichMovieDirectory(ctx context.Context, directory model.Entry) error {
	if p.provider == nil {
		return nil
	}
	root, err := p.catalog.IsLibrarySourceRoot(ctx, directory)
	if err != nil {
		return err
	}
	types, err := p.catalog.LibraryTypesForEntry(ctx, directory)
	if err != nil {
		return err
	}
	if root || !contains(types, "movies") {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	children, err := p.catalog.Children(ctx, directory.ID, catalog.ListOptions{Limit: 500})
	if err != nil {
		return err
	}
	if len(children) >= 500 { // The query is capped at 500; never infer from a possibly truncated directory.
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	var parsed ParsedName
	videoCount := 0
	for _, child := range children {
		if child.Type != model.EntryFile || !child.Available || !movieVideoExtension(child.Extension) || isAuxiliaryVideo(child.Name) {
			continue
		}
		candidate := ParseName(child.Name)
		if candidate.Title == "" {
			_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
			return err
		}
		if videoCount == 0 {
			parsed = candidate
		} else if normalizedTitle(moviePartTitle(parsed.Title)) != normalizedTitle(moviePartTitle(candidate.Title)) ||
			parsed.Year != nil && candidate.Year != nil && *parsed.Year != *candidate.Year {
			_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
			return err
		}
		videoCount++
	}
	if videoCount == 0 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	parsed.Title = moviePartTitle(parsed.Title)
	parentName := ParseName(directory.Name)
	if parentName.Title == "" {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	results, err := p.provider.Search(ctx, Query{Type: "movie", Title: parsed.Title, Year: parsed.Year, Language: p.language})
	if err != nil {
		return err // Network failure must not clear a previously resolved match.
	}
	match, fileScore, ok := bestCandidate(parsed, results)
	if !ok || fileScore < .8 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	_, folderScore, folderOK := bestCandidate(parentName, []Candidate{match})
	if !folderOK || folderScore < .8 {
		_, err := p.catalog.ClearMediaCandidate(ctx, directory.ID, "tmdb")
		return err
	}
	encoded, err := json.Marshal(match)
	if err != nil {
		return err
	}
	_, err = p.catalog.SetMediaCandidate(ctx, directory.ID, "tmdb", catalog.MediaCandidate{
		Kind: "movie", Title: match.Title, Year: match.Year, Summary: strings.TrimSpace(match.Overview), Data: encoded,
	})
	if err != nil {
		return err
	}
	if p.artwork != nil {
		return p.artwork.ProcessEntry(ctx, directory.ID)
	}
	return nil
}

func movieVideoExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv", "rmvb":
		return true
	default:
		return false
	}
}

func isAuxiliaryVideo(name string) bool {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	return strings.HasSuffix(stem, "-trailer") || strings.HasSuffix(stem, ".trailer") ||
		strings.HasSuffix(stem, "-sample") || strings.HasSuffix(stem, ".sample") ||
		strings.HasSuffix(stem, "预告") || strings.HasSuffix(stem, "样片")
}

var moviePartSuffix = regexp.MustCompile(`(?i)[ ._-]+(?:cd|disc|disk)[ ._-]*\d+$`)

func moviePartTitle(title string) string {
	return strings.TrimSpace(moviePartSuffix.ReplaceAllString(title, ""))
}
