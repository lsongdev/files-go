package enrichment

import (
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/processor"
)

// Sidecar runs last and updates the directory's own media record.
func Sidecar(enricher *mediaengine.DirectoryEnricher) processor.Plugin {
	return processor.NewPlugin("sidecar", enricher.Match, enricher)
}
