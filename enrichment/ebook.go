package enrichment

import (
	"github.com/lsongdev/files-go/catalog"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

func Ebook(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail) processor.Plugin {
	return processor.NewPlugin("ebook", func(entry model.Entry) bool {
		return extensionIn(entry, "epub")
	}, processor.NewEPUBMetadata(catalog, storages), thumbnail, mediaengine.NewCatalogerForKind(catalog, "book"))
}
