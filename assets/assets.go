package assets

import "embed"

//go:embed *.png
var Files embed.FS

const (
	ASSETS_FOLDER = "/assets/folder.png"
	ASSETS_FILE   = "/assets/file.png"
	ASSETS_MP3    = "/assets/mp3.png"
	ASSETS_VIDEO  = "/assets/video.png"
	ASSETS_EBOOK  = "/assets/ebook.png"
)
