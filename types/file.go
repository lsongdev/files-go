package types

import (
	"path/filepath"
	"time"
)

type File struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	IsDir     bool      `json:"isDir"`
	Path      string    `json:"path"`
	Icon      string    `json:"icon"`
	Title     string    `json:"title"`
	Line1     string    `json:"line1"`
	Line2     string    `json:"line2"`
	Line3     string    `json:"line3"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (f *File) FileName() string {
	return filepath.Join(f.Path, f.Name)
}
