package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

// These writers translate parsed file capabilities into the common display
// model. line1/line2/line3 are intentionally human-readable so clients do not
// need media-type-specific metadata parsing just to render a useful list.
func writePhotoMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, parsed model.ParsedMedia) error {
	if isDirectoryArtwork(entry.Name) {
		_, err := cat.ClearMediaCandidate(ctx, entry.ID, "embedded")
		return err
	}
	var metadata map[string]any
	if err := json.Unmarshal(parsed.Metadata, &metadata); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{
		"width": parsed.Width, "height": parsed.Height, "takenAt": parsed.TakenAt,
		"camera": parsed.Camera, "latitude": parsed.Latitude, "longitude": parsed.Longitude,
		"exif": json.RawMessage(parsed.Metadata),
	})
	if err != nil {
		return err
	}
	line1 := "照片"
	if parsed.Width != nil && parsed.Height != nil {
		line1 = fmt.Sprintf("照片 · %d × %d", *parsed.Width, *parsed.Height)
	}
	line3 := ""
	if parsed.TakenAt != nil {
		line3 = parsed.TakenAt.Local().Format("2006-01-02 15:04")
	} else if format, _ := metadata["format"].(string); format != "" {
		line3 = strings.ToUpper(format)
	}
	_, err = cat.SetMediaCandidate(ctx, entry.ID, "embedded", catalog.MediaCandidate{
		Kind: "photo", Title: fileStem(entry.Name), Icon: "file:" + entry.ID,
		Line1: line1, Line2: parsed.Camera, Line3: line3, Data: data,
	})
	return err
}

func writeBookMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, parsed model.ParsedMedia) error {
	var details struct {
		Title       string   `json:"title"`
		Authors     []string `json:"authors"`
		Language    string   `json:"language"`
		Publisher   string   `json:"publisher"`
		Description string   `json:"description"`
		Cover       struct {
			Path string `json:"path"`
		} `json:"cover"`
	}
	if err := json.Unmarshal(parsed.Metadata, &details); err != nil {
		return err
	}
	title := strings.TrimSpace(details.Title)
	if title == "" {
		title = fileStem(entry.Name)
	}
	line1 := strings.Join(details.Authors, "、")
	if line1 == "" {
		line1 = "电子书"
	}
	line2 := joinDisplay(details.Publisher, strings.ToUpper(details.Language))
	candidate := catalog.MediaCandidate{
		Kind: "book", Title: title, Line1: line1, Line2: line2, Line3: "EPUB",
		Summary: strings.TrimSpace(details.Description), Data: parsed.Metadata,
	}
	if details.Cover.Path != "" {
		candidate.Icon = "file:" + entry.ID
	}
	_, err := cat.SetMediaCandidate(ctx, entry.ID, "embedded", candidate)
	return err
}

func writePDFMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, data json.RawMessage) error {
	var details struct {
		Title      string `json:"title"`
		Author     string `json:"author"`
		Subject    string `json:"subject"`
		PageCount  int    `json:"pageCount"`
		PDFVersion string `json:"pdfVersion"`
	}
	if err := json.Unmarshal(data, &details); err != nil {
		return err
	}
	title := strings.TrimSpace(details.Title)
	if title == "" {
		title = fileStem(entry.Name)
	}
	line1 := strings.TrimSpace(details.Author)
	if line1 == "" {
		line1 = "PDF 文档"
	}
	line3Parts := []string{}
	if details.PageCount > 0 {
		line3Parts = append(line3Parts, fmt.Sprintf("%d 页", details.PageCount))
	}
	if details.PDFVersion != "" {
		line3Parts = append(line3Parts, "PDF "+details.PDFVersion)
	}
	_, err := cat.SetMediaCandidate(ctx, entry.ID, "embedded", catalog.MediaCandidate{
		Kind: "book", Title: title, Icon: "file:" + entry.ID,
		Line1: line1, Line2: strings.TrimSpace(details.Subject), Line3: strings.Join(line3Parts, " · "), Data: data,
	})
	return err
}

func writeAVMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, parsed model.ParsedMedia) error {
	technical := map[string]any{
		"durationMs": parsed.DurationMS, "container": parsed.Container,
		"width": parsed.Width, "height": parsed.Height, "videoCodec": parsed.VideoCodec,
		"audioCodec": parsed.AudioCodec, "bitrate": parsed.Bitrate,
	}
	var raw map[string]any
	if err := json.Unmarshal(parsed.Metadata, &raw); err != nil {
		return err
	}
	technical["probe"] = raw
	candidate := catalog.MediaCandidate{Title: fileStem(entry.Name)}
	if parsed.Kind == "audio" {
		candidate.Kind = "audio"
		candidate.Line1 = "音乐"
		if music, ok := raw["music"].(map[string]any); ok {
			if title := stringValue(music["title"]); title != "" {
				candidate.Title = title
			}
			artist := firstDisplay(stringValue(music["artist"]), stringValue(music["album_artist"]))
			album := stringValue(music["album"])
			candidate.Line1 = firstDisplay(artist, "音乐")
			candidate.Line2 = album
			candidate.Line3 = joinDisplay(
				trackLabel(stringValue(music["track"])),
				stringValue(music["date"]),
				compactDuration(parsed.DurationMS),
				strings.ToUpper(parsed.AudioCodec),
			)
			if music["hasAlbumArt"] == true {
				candidate.Icon = "file:" + entry.ID
			}
		}
	} else if parsed.Kind == "video" {
		candidate.Kind = "video"
		candidate.Line1 = "视频"
		if parsed.Width != nil && parsed.Height != nil {
			candidate.Line1 = fmt.Sprintf("视频 · %d × %d", *parsed.Width, *parsed.Height)
		}
		candidate.Line2 = joinDisplay(strings.ToUpper(parsed.VideoCodec), strings.ToUpper(parsed.AudioCodec))
		candidate.Line3 = joinDisplay(compactDuration(parsed.DurationMS), displayContainer(parsed.Container))
	}
	data, err := json.Marshal(technical)
	if err != nil {
		return err
	}
	candidate.Data = data
	_, err = cat.SetMediaCandidate(ctx, entry.ID, "embedded", candidate)
	return err
}

func fileStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func joinDisplay(values ...string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, " · ")
}

func firstDisplay(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func trackLabel(value string) string {
	if value == "" {
		return ""
	}
	number := strings.SplitN(value, "/", 2)[0]
	if _, err := strconv.Atoi(number); err == nil {
		return "Track " + number
	}
	return value
}

func compactDuration(durationMS *int64) string {
	if durationMS == nil || *durationMS <= 0 {
		return ""
	}
	seconds := *durationMS / 1000
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func displayContainer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if first, _, found := strings.Cut(value, ","); found {
		value = first
	}
	switch strings.ToLower(value) {
	case "matroska":
		return "MKV"
	case "mov", "mp4", "m4a", "3gp", "3g2", "mj2":
		return "MP4"
	default:
		return strings.ToUpper(value)
	}
}

func isDirectoryArtwork(name string) bool {
	value := strings.ToLower(name)
	switch value {
	case "folder.jpg", "folder.jpeg", "folder.png", "poster.jpg", "poster.jpeg", "poster.png",
		"cover.jpg", "cover.jpeg", "cover.png", "backdrop.jpg", "backdrop.jpeg", "backdrop.png",
		"fanart.jpg", "fanart.jpeg", "fanart.png", "background.jpg", "background.jpeg", "background.png":
		return true
	}
	extension := filepath.Ext(value)
	if extension != ".jpg" && extension != ".jpeg" && extension != ".png" {
		return false
	}
	stem := strings.TrimSuffix(value, extension)
	for _, suffix := range []string{"-poster", "-cover", "-backdrop", "-fanart", "-background"} {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	return false
}

var _ = time.Time{}
