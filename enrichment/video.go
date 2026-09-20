package enrichment

import (
	"time"

	"github.com/lsongdev/files-go/catalog"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

func Video(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail, enricher *mediaengine.VideoEnricher, artwork *mediaengine.Artwork, ffprobe, ffmpeg string) processor.Plugin {
	steps := []processor.Processor{
		processor.NewFFProbe(catalog, storages, ffprobe, 30*time.Second),
		processor.NewVideoThumbnail(catalog, storages, thumbnail, ffmpeg, 60*time.Second),
	}
	if enricher != nil {
		steps = append(steps, enricher)
	}
	if artwork != nil {
		steps = append(steps, artwork)
	}
	return processor.NewPlugin("video", func(entry model.Entry) bool {
		return extensionIn(entry, "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "flv", "wmv", "rmvb")
	}, steps...)
}
