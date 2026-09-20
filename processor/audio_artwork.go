package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type AudioArtwork struct {
	catalog   *catalog.Catalog
	storages  *storage.Registry
	thumbnail *Thumbnail
	binary    string
	timeout   time.Duration
}

func NewAudioArtwork(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *Thumbnail, binary string, timeout time.Duration) *AudioArtwork {
	if binary == "" {
		binary = "ffmpeg"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AudioArtwork{catalog: catalog, storages: storages, thumbnail: thumbnail, binary: binary, timeout: timeout}
}

func (p *AudioArtwork) Name() string { return "audio_artwork" }
func (p *AudioArtwork) Match(entry model.Entry) bool {
	if isMetadataSidecar(entry) {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp3", "m4a", "aac", "flac", "ogg", "opus", "wma", "aiff", "ape":
		return true
	default:
		return false
	}
}

func (p *AudioArtwork) Process(ctx context.Context, entry model.Entry) error {
	if _, err := p.catalog.ArtifactForEntry(ctx, entry.ID, "thumbnail", "large"); err == nil {
		return nil
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	mediaItem, err := p.catalog.MediaForEntry(ctx, entry.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if mediaItem.Kind != "audio" {
		return nil
	}
	var metadata struct {
		Embedded struct {
			Probe struct {
				Music struct {
					HasAlbumArt bool `json:"hasAlbumArt"`
				} `json:"music"`
			} `json:"probe"`
		} `json:"embedded"`
	}
	if json.Unmarshal(mediaItem.Data, &metadata) != nil || !metadata.Embedded.Probe.Music.HasAlbumArt {
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
	processCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(processCtx, p.binary, "-v", "error", "-i", filename,
		"-map", "0:v:0", "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	stdout, stderr := &limitedBuffer{limit: 20 << 20}, &limitedBuffer{limit: 64 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if processCtx.Err() != nil {
			return fmt.Errorf("audio artwork timeout: %w", processCtx.Err())
		}
		if stdout.exceeded || stderr.exceeded {
			return ErrProcessOutputTooLarge
		}
		return fmt.Errorf("ffmpeg audio artwork: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	imageValue, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return fmt.Errorf("decode audio artwork: %w", err)
	}
	return p.thumbnail.writeVariants(ctx, entry, imageValue)
}
