package processors

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/types"
)

type ImageProcessor struct{}

func (p *ImageProcessor) IsSupport(info *types.File) bool {
	ext := strings.ToLower(filepath.Ext(info.FileName()))
	return !info.IsDir && (ext == ".jpg" || ext == ".jpeg" || ext == ".png")
}

func (p *ImageProcessor) Process(info *types.File) error {
	info.Icon = fmt.Sprintf("/file?path=%s", info.FileName())
	return nil
}
