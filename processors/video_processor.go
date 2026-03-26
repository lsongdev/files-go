package processors

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lsongdev/fileinfo-go/fileinfo"
	"github.com/lsongdev/files-go/types"
)

type VideoProcessor struct{}

func (p *VideoProcessor) IsSupport(info *types.File) bool {
	ext := strings.ToLower(filepath.Ext(info.FileName()))
	videoExts := []string{".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".rmvb"}
	return !info.IsDir && contains(videoExts, ext)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (p *VideoProcessor) Process(f *types.File) error {
	f.Icon = "/assets/video.png"
	info := fileinfo.Parse(f.Name)
	// 调用 TMDB API 搜索
	tmdbInfo, _ := searchTMDB(info.Title)
	if tmdbInfo != nil {
		f.Title = info.Title
		if tmdbInfo.Name != "" {
			f.Title = tmdbInfo.Name
		}
		if tmdbInfo.Title != "" {
			f.Title = tmdbInfo.Title
		}
		f.Line1 = getReleaseDate(tmdbInfo)
		f.Line2 = fmt.Sprintf("Rating: %.1f", tmdbInfo.VoteAverage)
		f.Line3 = tmdbInfo.Overview
		f.Icon = "https://image.tmdb.org/t/p/w500" + tmdbInfo.PosterPath
		return nil
	}
	f.Title = info.Title
	return nil
}

func parseVideoName(filename string) string {
	// 移除常见的视频文件标记
	markers := []string{"1080p", "720p", "2160p", "4K", "BluRay", "WEB-DL", "HDTV", "x264", "x265", "HEVC", "AAC", "DTS"}
	for _, marker := range markers {
		filename = strings.ReplaceAll(filename, marker, "")
	}
	// 清理多余的空格和点
	filename = strings.ReplaceAll(filename, ".", " ")
	filename = strings.ReplaceAll(filename, "_", " ")
	// 移除年份（通常是 4 位数字）
	// filename = regexp.MustCompile(`\b(19|20)\d{2}\b`).ReplaceAllString(filename, "")
	return strings.TrimSpace(filename)
}
