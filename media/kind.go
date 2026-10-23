package media

import (
	"path"
	"strings"
)

var kinds = map[string][]string{
	"video":    {"mp4", "m4v", "mkv", "webm", "mov", "avi", "wmv", "flv", "f4v", "rmvb", "rm", "mpg", "mpeg", "m2ts", "mts", "ts", "3gp", "ogv", "vob", "asf"},
	"audio":    {"mp3", "flac", "wav", "aac", "m4a", "m4b", "ogg", "oga", "opus", "wma", "ape", "aif", "aiff", "alac", "dsf", "dff", "mka", "amr"},
	"image":    {"jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif", "svg", "ico", "psd", "cr2", "nef", "arw", "dng", "raf", "orf"},
	"book":     {"epub", "mobi", "azw", "azw3", "fb2", "djvu", "cbz", "cbr"},
	"document": {"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "pages", "numbers", "key", "wps", "et", "dps"},
	"text":     {"txt", "md", "markdown", "log", "csv", "tsv", "json", "xml", "yaml", "yml", "toml", "ini", "conf", "cfg", "srt", "ass", "ssa", "vtt", "lrc", "nfo", "rst", "tex"},
	"code":     {"go", "js", "mjs", "cjs", "jsx", "tsx", "py", "c", "h", "cc", "cpp", "hpp", "java", "kt", "rs", "swift", "rb", "php", "sh", "bash", "zsh", "css", "scss", "less", "html", "htm", "vue", "svelte", "sql", "lua", "pl", "r", "dart", "m", "mm", "cs", "ino", "cmake", "gradle", "make", "proto"},
	"archive":  {"zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "lz", "lzma", "iso", "img", "dmg", "cab", "jar", "war"},
	"app":      {"exe", "msi", "dll", "sys", "apk", "xapk", "aab", "ipa", "deb", "rpm", "appimage", "pkg", "elf", "so", "dylib", "bin", "app"},
	"font":     {"ttf", "otf", "woff", "woff2", "ttc", "fon"},
	"torrent":  {"torrent"},
}

var kindOf = func() map[string]string {
	m := map[string]string{}
	for kind, exts := range kinds {
		for _, ext := range exts {
			m[ext] = kind
		}
	}
	return m
}()

// Ext returns the lowercase extension without the dot.
func Ext(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

// Classify guesses a file kind from its name and size without opening it.
func Classify(name string, size int64) string {
	ext := Ext(name)
	switch {
	case strings.HasPrefix(name, "._"):
		return "" // macOS resource fork
	case ext == "ts" || ext == "mts" || ext == "cts":
		// TypeScript shares these extensions; real transport streams are big.
		// The probe enricher confirms the sync byte before treating it as video.
		if size >= 256<<10 && !strings.HasSuffix(strings.ToLower(name), ".d.ts") && ext != "cts" {
			return "video"
		}
		return "code"
	case ext == "m":
		return "code"
	}
	return kindOf[ext]
}
