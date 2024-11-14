package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/lsongdev/apk-go/apk"
	v2 "github.com/lsongdev/id3-go/v2"
)

func (s *FileServer) initProcessors() {
	s.processors = []FileProcessor{
		&MusicProcessor{},
		&ImageProcessor{},
		&APKProcessor{},
		&ImageProcessor{},
		&DefaultProcessor{},
	}
}

func (s *FileServer) GetProcessor(file *File) (processor FileProcessor) {
	for _, p := range s.processors {
		if p.IsSupport(file) {
			return p
		}
	}
	return &DefaultProcessor{}
}

type FileProcessor interface {
	IsSupport(file *File) bool
	Process(file *File) error
}

type MusicProcessor struct{}

func (p *MusicProcessor) IsSupport(info *File) bool {
	ext := strings.ToLower(filepath.Ext(info.filename()))
	return !info.IsDir && ext == ".mp3"
}

func (p *MusicProcessor) Process(info *File) error {
	f, err := os.Open(info.filename())
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
	// info.Line2 = tag.Album()
	// info.Line3 = tag.Genre()
	info.Icon = "https://cdn-icons-png.flaticon.com/512/4039/4039628.png"
	return nil
}

type ImageProcessor struct{}

func (p *ImageProcessor) IsSupport(info *File) bool {
	ext := strings.ToLower(filepath.Ext(info.filename()))
	return !info.IsDir && (ext == ".jpg" || ext == ".jpeg" || ext == ".png")
}

func (p *ImageProcessor) Process(info *File) error {
	info.Icon = fmt.Sprintf("/file?path=%s", info.filename())
	return nil
}

type APKProcessor struct {
}

func (p *APKProcessor) IsSupport(info *File) bool {
	ext := strings.ToLower(filepath.Ext(info.filename()))
	return !info.IsDir && ext == ".apk"
}

func (p *APKProcessor) Process(info *File) error {
	pkg, err := apk.Open(info.filename())
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

type DefaultProcessor struct{}

func (p *DefaultProcessor) IsSupport(info *File) bool {
	return true // 默认处理器支持所有文件
}

func (p *DefaultProcessor) Process(info *File) (err error) {
	if info.IsDir {
		info.Icon = "https://cdn-icons-png.freepik.com/256/12532/12532956.png"
	} else {
		info.Icon = "https://cdn-icons-png.flaticon.com/256/607/607674.png" // 默认图标
	}
	return
}
