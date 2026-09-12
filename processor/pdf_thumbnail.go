package processor

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type PDFThumbnail struct {
	catalog   *catalog.Catalog
	storages  *storage.Registry
	thumbnail *Thumbnail
	cacheDir  string
	binary    string
	timeout   time.Duration
}

func NewPDFThumbnail(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *Thumbnail, cacheDir, binary string, timeout time.Duration) *PDFThumbnail {
	if binary == "" {
		binary = "pdftoppm"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &PDFThumbnail{catalog: catalog, storages: storages, thumbnail: thumbnail, cacheDir: cacheDir, binary: binary, timeout: timeout}
}

func (p *PDFThumbnail) Name() string { return "pdf_thumbnail" }
func (p *PDFThumbnail) Match(entry model.Entry) bool {
	return !isMetadataSidecar(entry) && strings.EqualFold(entry.Extension, "pdf")
}

func (p *PDFThumbnail) Process(ctx context.Context, entry model.Entry) error {
	if _, err := p.catalog.ArtifactForEntry(ctx, entry.ID, "thumbnail", "large"); err == nil {
		return nil
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	mediaFile, err := p.catalog.MediaFile(ctx, entry.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if mediaFile.Kind != "book" || mediaFile.Container != "pdf" {
		return nil
	}
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	native, ok := backend.(storage.NativePather)
	if !ok {
		return storage.ErrUnsupported
	}
	filename, err := native.NativePath(ctx, entry.Path)
	if err != nil {
		return err
	}
	temporaryRoot := filepath.Join(p.cacheDir, "tmp")
	if err := os.MkdirAll(temporaryRoot, 0755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(temporaryRoot, "pdf-thumbnail-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	prefix := filepath.Join(temporary, "page")
	processCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(processCtx, p.binary, "-f", "1", "-l", "1", "-singlefile", "-scale-to", "1280", "-jpeg", "-jpegopt", "quality=86", filename, prefix)
	stderr := &limitedBuffer{limit: 64 << 10}
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if processCtx.Err() != nil {
			return fmt.Errorf("PDF thumbnail timeout: %w", processCtx.Err())
		}
		if stderr.exceeded {
			return ErrProcessOutputTooLarge
		}
		return fmt.Errorf("pdftoppm: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	file, err := os.Open(prefix + ".jpg")
	if err != nil {
		return err
	}
	imageValue, _, decodeErr := image.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode PDF thumbnail: %w", decodeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return p.thumbnail.writeVariants(ctx, entry, imageValue)
}
