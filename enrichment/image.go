package enrichment

import (
	"github.com/lsongdev/files-go/catalog"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

// Image extracts EXIF and thumbnails before attaching ordinary photos to a
// media identity. Folder artwork is excluded from that identity by Cataloger.
func Image(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail) processor.Plugin {
	return processor.NewPlugin("image", func(entry model.Entry) bool {
		return extensionIn(entry, "jpg", "jpeg", "png", "gif")
	}, processor.NewImageMetadata(catalog, storages), thumbnail, mediaengine.NewCatalogerForKind(catalog, "photo"))
}
