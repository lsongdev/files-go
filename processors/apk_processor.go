package processors

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/lsongdev/apk-go/apk"
	"github.com/lsongdev/files-go/types"
)

type APKProcessor struct{}

func (p *APKProcessor) IsSupport(info *types.File) bool {
	ext := strings.ToLower(filepath.Ext(info.FileName()))
	return !info.IsDir && ext == ".apk"
}

func (p *APKProcessor) Process(info *types.File) error {
	pkg, err := apk.Open(info.FileName())
	if err != nil {
		return err
	}
	defer pkg.Close()
	packageName := pkg.PackageName()
	info.Title, _ = pkg.Label(nil)
	info.Line1 = packageName
	icon, err := pkg.Icon(nil)
	if err != nil {
		return err
	}
	tmpfile := fmt.Sprintf("/tmp/%s.png", packageName)
	f, err := os.Create(tmpfile)
	if err != nil {
		return err
	}
	defer f.Close()
	info.Icon = fmt.Sprintf("/file?path=%s", tmpfile)
	return png.Encode(f, icon)
}
