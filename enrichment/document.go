package enrichment

import (
	"time"

	"github.com/lsongdev/files-go/catalog"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

func Document(catalog *catalog.Catalog, storages *storage.Registry, thumbnail *processor.Thumbnail, cacheDir, pdfInfo, pdfToPPM string) processor.Plugin {
	return processor.NewPlugin("document", func(entry model.Entry) bool {
		return extensionIn(entry, "pdf")
	}, processor.NewPDFMetadata(catalog, storages, pdfInfo, 30*time.Second),
		processor.NewPDFThumbnail(catalog, storages, thumbnail, cacheDir, pdfToPPM, 60*time.Second), mediaengine.NewCatalogerForKind(catalog, "book"))
}
