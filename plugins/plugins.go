// Package plugins composes reusable processors into product-level media plugins.
// Each plugin owns its matcher and ordered capabilities; Engine only schedules work.
package plugins

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
