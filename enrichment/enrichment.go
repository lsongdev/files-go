// Package enrichment assembles media-specific processing plugins. Each plugin
// owns its file matcher and ordered steps; the engine only schedules entries.
package enrichment

import (
	"strings"

	"github.com/lsongdev/files-go/model"
)

func extensionIn(entry model.Entry, extensions ...string) bool {
	if entry.Type != model.EntryFile || strings.HasPrefix(entry.Name, "._") || strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	for _, extension := range extensions {
		if strings.EqualFold(entry.Extension, extension) {
			return true
		}
	}
	return false
}
