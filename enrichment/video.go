package enrichment

import (
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

// Video extracts file-level technical metadata and a seekable preview image.
// Movie/TV recognition deliberately lives in the separate movies plugin.
func Video(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail, ffprobe, ffmpeg string) processor.Plugin {
	return processor.NewPlugin("video", func(entry model.Entry) bool {
		return extensionIn(entry, "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "flv", "wmv", "rmvb")
	},
		processor.NewFFProbe(catalog, storages, ffprobe, 30*time.Second),
		processor.NewVideoThumbnail(catalog, storages, thumbnail, ffmpeg, 60*time.Second),
	)
}
