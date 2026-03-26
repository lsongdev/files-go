package processors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lsongdev/epub-go/epub"
	"github.com/lsongdev/files-go/types"
)

type EpubProcessor struct{}

func (p *EpubProcessor) IsSupport(info *types.File) bool {
	ext := strings.ToLower(filepath.Ext(info.FileName()))
	return !info.IsDir && ext == ".epub"
}

func (p *EpubProcessor) Process(info *types.File) error {
	info.Icon = "/assets/ebook.png"
	book, err := epub.Open(info.FileName())
	if err != nil {
		return err
	}
	defer book.Close()
	info.Title = book.Title()
	info.Line1 = book.Author()
	cover, err := book.ReadCover()
	if err == nil {
		tmpfile := fmt.Sprintf("/tmp/%x.png", info.Name)
		info.Icon = fmt.Sprintf("/file?path=%s", tmpfile)
		os.WriteFile(tmpfile, cover, 0644)
	}
	return nil
}
