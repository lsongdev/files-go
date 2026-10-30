package media

import (
	"archive/tar"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/lsongdev/files-go/store"
)

// Archive summarizes ZIP archives: entry count and uncompressed size.
type Archive struct{ Open Opener }

func (Archive) Match(f *store.File) bool {
	ext := Ext(f.Name)
	return f.Kind == "archive" && (ext == "zip" || ext == "jar")
}

func (a Archive) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	input, err := a.Open(ctx, *f)
	if err != nil {
		return err
	}
	defer input.Close()
	m.Kind = "archive"
	err = withZip(input, func(z *zip.Reader) error {
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
	if err == nil {
		return nil
	}
	input.Seek(0, 0)
	r := tar.NewReader(input)
	files := 0
	var size int64
	headers := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, readErr := r.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return err
		}
		headers++
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			files++
			size += h.Size
		}
	}
	if headers == 0 {
		return err
	}
	m.Set("Format", "TAR")
	m.Set("Files", strconv.Itoa(files))
	m.Set("Uncompressed", fmt.Sprintf("%.1f MB", float64(size)/(1<<20)))
	return nil
}
