package plugins

import (
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

func Ebook(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail) Plugin {
	return NewPlugin("ebook", func(entry model.Entry) bool {
		return extensionIn(entry, "epub")
	}, processor.NewEPUBMetadata(catalog, storages), thumbnail)
}
