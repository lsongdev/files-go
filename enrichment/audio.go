package enrichment

import (
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

func Audio(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail, ffprobe, ffmpeg string) processor.Plugin {
	return processor.NewPlugin("audio", func(entry model.Entry) bool {
		return extensionIn(entry, "mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "wma", "aiff", "ape")
	}, processor.NewFFProbe(catalog, storages, ffprobe, 30*time.Second),
		processor.NewAudioArtwork(catalog, storages, thumbnail, ffmpeg, 30*time.Second))
}
