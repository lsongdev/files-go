package processor

import (
	"context"
	"strings"

	"github.com/lsongdev/files-go/model"
)

// Processor is one reusable file-processing capability. Plugins compose these
// capabilities; processors do not know about plugin ordering or configuration.
type Processor interface {
	Name() string
	Match(model.Entry) bool
	Process(context.Context, model.Entry) error
}

func isMetadataSidecar(entry model.Entry) bool {
	return strings.HasPrefix(entry.Name, "._")
}
