package processors

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/assets"
	"github.com/lsongdev/files-go/types"
	v2 "github.com/lsongdev/id3-go/v2"
)

type MusicProcessor struct{}

func (p *MusicProcessor) IsSupport(info *types.File) bool {
	ext := strings.ToLower(filepath.Ext(info.FileName()))
	return !info.IsDir && ext == ".mp3"
}

func (p *MusicProcessor) Process(info *types.File) error {
	info.Icon = assets.ASSETS_MP3
	f, err := os.Open(info.FileName())
	if err != nil {
		return err
	}
	defer f.Close()
	tag, err := v2.Read(f)
	if err != nil {
		return err
	}
	info.Title = tag.Title()
	info.Line1 = tag.Artist()
	info.Line2 = tag.Album()
	info.Line3 = tag.Genre()
	return nil
}
