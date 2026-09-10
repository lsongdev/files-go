package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

var thumbnailVariants = map[string]int{"small": 256, "medium": 640, "large": 1280}

type Thumbnail struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
	cacheDir string
}

func NewThumbnail(catalog *catalog.Catalog, storages *storage.Registry, cacheDir string) *Thumbnail {
	return &Thumbnail{catalog: catalog, storages: storages, cacheDir: cacheDir}
}

func (p *Thumbnail) Name() string { return "thumbnail" }

func (p *Thumbnail) Match(entry model.Entry) bool {
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

func (p *Thumbnail) Process(ctx context.Context, entry model.Entry) error {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	file, err := backend.Open(ctx, entry.Path)
	if err != nil {
		return err
	}
	config, _, err := image.DecodeConfig(contextReader{ctx: ctx, reader: file})
	if err != nil {
		_ = file.Close()
		return err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 50_000_000 {
		_ = file.Close()
		return fmt.Errorf("image dimensions exceed thumbnail limit: %dx%d", config.Width, config.Height)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return err
	}
	imageValue, _, err := image.Decode(contextReader{ctx: ctx, reader: file})
	_ = file.Close()
	if err != nil {
		return err
	}
	current := imageValue
	for _, item := range []struct {
		variant string
		maximum int
	}{{"large", 1280}, {"medium", 640}, {"small", 256}} {
		if err := ctx.Err(); err != nil {
			return err
		}
		resized, err := resizeArea(ctx, current, item.maximum)
		if err != nil {
			return err
		}
		current = resized
		key := thumbnailKey(entry, item.variant)
		filename, err := ThumbnailPath(p.cacheDir, key)
		if err != nil {
			return err
		}
		if err := writeJPEG(filename, resized); err != nil {
			return err
		}
		info, err := os.Stat(filename)
		if err != nil {
			return err
		}
		if _, err := p.catalog.UpsertArtifact(ctx, model.Artifact{
			EntryID: entry.ID, Type: "thumbnail", Variant: item.variant, Key: key, MIME: "image/jpeg", Size: info.Size(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func thumbnailKey(entry model.Entry, variant string) string {
	value := fmt.Sprintf("%s:%d:%d:thumbnail:%s", entry.ID, entry.ModifiedAt.UnixNano(), entry.Size, variant)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func ThumbnailPath(cacheDir, key string) (string, error) {
	if len(key) != 64 {
		return "", errorsNewInvalidArtifactKey()
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", errorsNewInvalidArtifactKey()
	}
	return filepath.Join(cacheDir, "thumbnails", key[:2], key[2:4], key+".jpg"), nil
}

func errorsNewInvalidArtifactKey() error { return fmt.Errorf("invalid artifact key") }

func resizeArea(ctx context.Context, source image.Image, maximum int) (image.Image, error) {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	destinationWidth, destinationHeight := sourceWidth, sourceHeight
	if sourceWidth > maximum || sourceHeight > maximum {
		destinationWidth, destinationHeight = maximum, maximum
	}
	if (sourceWidth > maximum || sourceHeight > maximum) && sourceWidth >= sourceHeight {
		destinationHeight = max(1, sourceHeight*maximum/sourceWidth)
	} else if sourceWidth > maximum || sourceHeight > maximum {
		destinationWidth = max(1, sourceWidth*maximum/sourceHeight)
	}
	destination := image.NewRGBA(image.Rect(0, 0, destinationWidth, destinationHeight))
	for y := range destinationHeight {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceY0 := bounds.Min.Y + y*sourceHeight/destinationHeight
		sourceY1 := bounds.Min.Y + (y+1)*sourceHeight/destinationHeight
		if sourceY1 <= sourceY0 {
			sourceY1 = sourceY0 + 1
		}
		for x := range destinationWidth {
			sourceX0 := bounds.Min.X + x*sourceWidth/destinationWidth
			sourceX1 := bounds.Min.X + (x+1)*sourceWidth/destinationWidth
			if sourceX1 <= sourceX0 {
				sourceX1 = sourceX0 + 1
			}
			var red, green, blue, alpha, count uint64
			for sourceY := sourceY0; sourceY < sourceY1; sourceY++ {
				for sourceX := sourceX0; sourceX < sourceX1; sourceX++ {
					r, g, b, a := source.At(sourceX, sourceY).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
					count++
				}
			}
			averageAlpha := alpha / count
			white := uint64(0xffff) - averageAlpha
			destination.SetRGBA64(x, y, color.RGBA64{
				R: uint16(min(uint64(0xffff), red/count+white)),
				G: uint16(min(uint64(0xffff), green/count+white)),
				B: uint16(min(uint64(0xffff), blue/count+white)), A: 0xffff,
			})
		}
	}
	return destination, nil
}

func writeJPEG(filename string, imageValue image.Image) (err error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".thumbnail-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
	if err := jpeg.Encode(file, imageValue, &jpeg.Options{Quality: 84}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}
