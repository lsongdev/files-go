package processor

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

const (
	maxEPUBSize  = 512 << 20
	maxEPUBXML   = 2 << 20
	maxEPUBFiles = 10_000
)

type EPUBMetadata struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
}

func NewEPUBMetadata(catalog *catalog.Catalog, storages *storage.Registry) *EPUBMetadata {
	return &EPUBMetadata{catalog: catalog, storages: storages}
}

func (p *EPUBMetadata) Name() string { return "epub_metadata" }
func (p *EPUBMetadata) Match(entry model.Entry) bool {
	return !isMetadataSidecar(entry) && strings.EqualFold(entry.Extension, "epub")
}

func (p *EPUBMetadata) Process(ctx context.Context, entry model.Entry) error {
	if entry.Size <= 0 || entry.Size > maxEPUBSize {
		return fmt.Errorf("EPUB size exceeds processing limit: %d", entry.Size)
	}
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	file, err := backend.Open(ctx, entry.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	readerAt, ok := file.(io.ReaderAt)
	if !ok {
		return storage.ErrUnsupported
	}
	reader, err := zip.NewReader(readerAt, entry.Size)
	if err != nil {
		return fmt.Errorf("open EPUB: %w", err)
	}
	if len(reader.File) > maxEPUBFiles {
		return errors.New("EPUB contains too many files")
	}
	rootData, err := readZIPFile(ctx, reader.File, "META-INF/container.xml", maxEPUBXML)
	if err != nil {
		return err
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(rootData, &container); err != nil || len(container.Rootfiles) == 0 {
		return errors.New("EPUB container has no rootfile")
	}
	opfPath, err := safeArchivePath(container.Rootfiles[0].FullPath)
	if err != nil {
		return err
	}
	opfData, err := readZIPFile(ctx, reader.File, opfPath, maxEPUBXML)
	if err != nil {
		return err
	}
	var publication struct {
		Metadata struct {
			Title       string   `xml:"title"`
			Creators    []string `xml:"creator"`
			Language    string   `xml:"language"`
			Publisher   string   `xml:"publisher"`
			Identifier  string   `xml:"identifier"`
			Description string   `xml:"description"`
			Meta        []struct {
				Name     string `xml:"name,attr"`
				Content  string `xml:"content,attr"`
				Property string `xml:"property,attr"`
				Value    string `xml:",chardata"`
			} `xml:"meta"`
		} `xml:"metadata"`
		Manifest []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
	}
	if err := xml.Unmarshal(opfData, &publication); err != nil {
		return fmt.Errorf("decode EPUB package: %w", err)
	}
	metadata := map[string]any{
		"title":       strings.TrimSpace(publication.Metadata.Title),
		"authors":     trimNonEmpty(publication.Metadata.Creators),
		"language":    strings.TrimSpace(publication.Metadata.Language),
		"publisher":   strings.TrimSpace(publication.Metadata.Publisher),
		"identifier":  strings.TrimSpace(publication.Metadata.Identifier),
		"description": strings.TrimSpace(publication.Metadata.Description),
	}
	coverID := ""
	for _, meta := range publication.Metadata.Meta {
		if strings.EqualFold(meta.Name, "cover") {
			coverID = strings.TrimSpace(meta.Content)
		}
	}
	for _, item := range publication.Manifest {
		if containsWord(item.Properties, "cover-image") || item.ID == coverID {
			if coverPath, err := safeArchivePath(path.Join(path.Dir(opfPath), item.Href)); err == nil {
				metadata["cover"] = map[string]string{"path": coverPath, "mediaType": item.MediaType}
			}
			break
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return p.catalog.UpsertMediaFile(ctx, model.MediaFile{EntryID: entry.ID, Kind: "book", Container: "epub", Metadata: encoded})
}

func readZIPFile(ctx context.Context, files []*zip.File, name string, limit int64) ([]byte, error) {
	for _, item := range files {
		clean, err := safeArchivePath(item.Name)
		if err != nil || clean != name {
			continue
		}
		if item.UncompressedSize64 > uint64(limit) {
			return nil, errors.New("EPUB XML exceeds processing limit")
		}
		reader, err := item.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: reader}, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("EPUB XML exceeds processing limit")
		}
		return data, nil
	}
	return nil, fmt.Errorf("EPUB file not found: %s", name)
}

func safeArchivePath(value string) (string, error) {
	clean := path.Clean(strings.TrimPrefix(value, "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("EPUB archive path escapes root")
	}
	return clean, nil
}

func containsWord(value, wanted string) bool {
	for _, word := range strings.Fields(value) {
		if word == wanted {
			return true
		}
	}
	return false
}
func trimNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
