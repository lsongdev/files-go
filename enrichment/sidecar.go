package enrichment

import (
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/processor"
)

// Sidecar runs last. A local NFO or artwork can override online metadata and
// also reconcile a video's containing folder independently of scan order.
func Sidecar(sidecar *mediaengine.Sidecar) processor.Plugin {
	return processor.NewPlugin("sidecar", sidecar.Match, sidecar)
}
