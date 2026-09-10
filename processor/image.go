package processor

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type ImageMetadata struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
}

func NewImageMetadata(catalog *catalog.Catalog, storages *storage.Registry) *ImageMetadata {
	return &ImageMetadata{catalog: catalog, storages: storages}
}

func (p *ImageMetadata) Name() string { return "image_metadata" }

func (p *ImageMetadata) Match(entry model.Entry) bool {
	if isMetadataSidecar(entry) {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "jpg", "jpeg", "png", "gif":
		return true
	default:
		return false
	}
}

func (p *ImageMetadata) Process(ctx context.Context, entry model.Entry) error {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	file, err := backend.Open(ctx, entry.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 100_000 || config.Height > 100_000 {
		return errors.New("invalid image dimensions")
	}
	metadata, err := json.Marshal(map[string]any{"format": format})
	if err != nil {
		return err
	}
	width, height := config.Width, config.Height
	return p.catalog.UpsertMediaFile(ctx, model.MediaFile{
		EntryID: entry.ID, Kind: "photo", Width: &width, Height: &height, Metadata: metadata,
	})
}

type contextReader struct {
	ctx    context.Context
	reader interface{ Read([]byte) (int, error) }
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
