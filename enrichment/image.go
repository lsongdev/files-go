package enrichment

import (
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

// Image writes each ordinary photo's own file-centric media record.
func Image(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail) processor.Plugin {
	return processor.NewPlugin("image", func(entry model.Entry) bool {
		return extensionIn(entry, "jpg", "jpeg", "png", "gif")
	}, processor.NewImageMetadata(catalog, storages), thumbnail)
}
