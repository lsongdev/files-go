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
	"time"

	"github.com/rwcarlsen/goexif/exif"

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
	mediaFile, err := p.inspect(ctx, entry)
	if err != nil {
		return err
	}
	return writePhotoMedia(ctx, p.catalog, entry, mediaFile)
}

func (p *ImageMetadata) inspect(ctx context.Context, entry model.Entry) (model.ParsedMedia, error) {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return model.ParsedMedia{}, storage.ErrOffline
	}
	file, err := backend.Open(ctx, entry.Path)
	if err != nil {
		return model.ParsedMedia{}, err
	}
	defer file.Close()
	metadata := map[string]any{}
	var takenAt *time.Time
	var latitude, longitude *float64
	camera := ""
	if document, decodeErr := exif.Decode(contextReader{ctx: ctx, reader: file}); decodeErr == nil {
		if value, dateErr := document.DateTime(); dateErr == nil {
			value = value.UTC()
			takenAt = &value
			metadata["takenAt"] = value.Format(time.RFC3339)
		}
		makeName := exifString(document, exif.Make)
		modelName := exifString(document, exif.Model)
		camera = strings.TrimSpace(strings.Join(trimNonEmpty([]string{makeName, modelName}), " "))
		for key, value := range map[string]string{"make": makeName, "model": modelName, "lens": exifString(document, exif.LensModel), "orientation": exifValue(document, exif.Orientation), "iso": exifValue(document, exif.ISOSpeedRatings), "exposureTime": exifValue(document, exif.ExposureTime), "fNumber": exifValue(document, exif.FNumber)} {
			if value != "" {
				metadata[key] = value
			}
		}
		if lat, long, gpsErr := document.LatLong(); gpsErr == nil {
			latitude, longitude = &lat, &long
			metadata["latitude"], metadata["longitude"] = lat, long
		}
	}
	if _, err := file.Seek(0, 0); err != nil {
		return model.ParsedMedia{}, err
	}
	config, format, err := image.DecodeConfig(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return model.ParsedMedia{}, err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 100_000 || config.Height > 100_000 {
		return model.ParsedMedia{}, errors.New("invalid image dimensions")
	}
	metadata["format"] = format
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return model.ParsedMedia{}, err
	}
	width, height := config.Width, config.Height
	return model.ParsedMedia{
		EntryID: entry.ID, Kind: "photo", Width: &width, Height: &height, TakenAt: takenAt,
		Camera: camera, Latitude: latitude, Longitude: longitude, Metadata: encoded,
	}, nil
}

func exifString(document *exif.Exif, name exif.FieldName) string {
	tag, err := document.Get(name)
	if err != nil {
		return ""
	}
	value, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func exifValue(document *exif.Exif, name exif.FieldName) string {
	tag, err := document.Get(name)
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(tag.String()), "\"")
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
