package processors

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lsongdev/files-go/types"
)

type DefaultProcessor struct{}

func (p *DefaultProcessor) IsSupport(info *types.File) bool {
	return true // 默认处理器支持所有文件
}

func (p *DefaultProcessor) Process(info *types.File) (err error) {
	if info.IsDir {
		info.Icon = "/assets/folder.png"
		icon := filepath.Join(info.FileName(), "folder.jpg")
		if _, err := os.Stat(icon); err == nil {
			info.Icon = fmt.Sprintf("/file?path=%s", icon)
		}
	} else {
		info.Icon = "/assets/file.png" // 默认图标
	}
	return
}
