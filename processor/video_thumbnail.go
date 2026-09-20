package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type VideoThumbnail struct {
	catalog   *catalog.Catalog
	storages  *storage.Registry
	thumbnail *Thumbnail
	binary    string
	timeout   time.Duration
}

func NewVideoThumbnail(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *Thumbnail, binary string, timeout time.Duration) *VideoThumbnail {
	if binary == "" {
		binary = "ffmpeg"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &VideoThumbnail{catalog: catalog, storages: storages, thumbnail: thumbnail, binary: binary, timeout: timeout}
}

func (p *VideoThumbnail) Name() string { return "video_thumbnail" }

func (p *VideoThumbnail) Match(entry model.Entry) bool {
	if isMetadataSidecar(entry) || strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "flv", "wmv", "rmvb":
		return true
	default:
		return false
	}
}

func (p *VideoThumbnail) Process(ctx context.Context, entry model.Entry) error {
	if _, err := p.catalog.ArtifactForEntry(ctx, entry.ID, "thumbnail", "large"); err == nil {
		return p.markScreenshot(ctx, entry)
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
	seekSeconds := 10.0
	var technical struct {
		Embedded struct {
			DurationMS *int64 `json:"durationMs"`
		} `json:"embedded"`
	}
	_ = json.Unmarshal(mediaItem.Data, &technical)
	if technical.Embedded.DurationMS != nil && *technical.Embedded.DurationMS > 0 {
		seekSeconds = min(300, max(5, float64(*technical.Embedded.DurationMS)/10_000))
	}
	processCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(processCtx, p.binary,
		"-v", "error", "-ss", strconv.FormatFloat(seekSeconds, 'f', 3, 64), "-i", filename,
		"-map", "0:v:0", "-frames:v", "1", "-vf", "scale='min(1280,iw)':-2", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	stdout, stderr := &limitedBuffer{limit: 20 << 20}, &limitedBuffer{limit: 64 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if processCtx.Err() != nil {
			return fmt.Errorf("video thumbnail timeout: %w", processCtx.Err())
		}
		if stdout.exceeded || stderr.exceeded {
			return ErrProcessOutputTooLarge
		}
		return fmt.Errorf("ffmpeg thumbnail: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	imageValue, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return fmt.Errorf("decode video thumbnail: %w", err)
	}
	if err := p.thumbnail.writeVariants(ctx, entry, imageValue); err != nil {
		return err
	}
	return p.markScreenshot(ctx, entry)
}

func (p *VideoThumbnail) markScreenshot(ctx context.Context, entry model.Entry) error {
	_, err := p.catalog.SetMediaCandidate(ctx, entry.ID, "screenshot", catalog.MediaCandidate{Icon: "file:" + entry.ID})
	return err
}
