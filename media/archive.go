package media

import (
	"archive/zip"
	"context"
	"fmt"
	"strconv"

	"github.com/lsongdev/files-go/store"
)

// Archive summarizes ZIP archives: entry count and uncompressed size.
type Archive struct{ Path Resolver }

func (Archive) Match(f *store.File) bool {
	ext := Ext(f.Name)
	return f.Kind == "archive" && (ext == "zip" || ext == "jar")
}

func (a Archive) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	name, err := a.Path(*f)
	if err != nil {
		return err
	}
	m.Kind = "archive"
	return withZip(name, func(z *zip.Reader) error {
		var files int
		var size uint64
		for _, entry := range z.File {
			if !entry.FileInfo().IsDir() {
				files++
				size += entry.UncompressedSize64
			}
		}
		m.Set("Files", strconv.Itoa(files))
		m.Set("Uncompressed", fmt.Sprintf("%.1f MB", float64(size)/(1<<20)))
		return nil
	})
}
