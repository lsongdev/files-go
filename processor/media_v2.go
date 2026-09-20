package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

// These writers translate the parsed file itself to the file-centric media
// record. A network-backed file is opened only once per processing job.
func writePhotoMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, parsed model.ParsedMedia) error {
	if isDirectoryArtwork(entry.Name) {
		_, err := cat.ClearMediaCandidate(ctx, entry.ID, "embedded")
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
	_, err = cat.SetMediaCandidate(ctx, entry.ID, "embedded", catalog.MediaCandidate{
		Kind: "photo", Title: fileStem(entry.Name), Icon: "file:" + entry.ID,
		Line1: line1, Line2: parsed.Camera, Data: data,
	})
	return err
}

func writeBookMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, parsed model.ParsedMedia) error {
	var details struct {
		Title   string   `json:"title"`
		Authors []string `json:"authors"`
		Cover   struct {
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
	candidate := catalog.MediaCandidate{
		Kind: "book", Title: title, Line1: "电子书 · EPUB",
		Line2: strings.Join(details.Authors, "、"), Data: parsed.Metadata,
	}
	if details.Cover.Path != "" {
		candidate.Icon = "file:" + entry.ID
	}
	_, err := cat.SetMediaCandidate(ctx, entry.ID, "embedded", candidate)
	return err
}

func writePDFMedia(ctx context.Context, cat *catalog.Catalog, entry model.Entry, data json.RawMessage) error {
	var details struct {
		Title  string `json:"title"`
		Author string `json:"author"`
	}
	if err := json.Unmarshal(data, &details); err != nil {
		return err
	}
	title := strings.TrimSpace(details.Title)
	if title == "" {
		title = fileStem(entry.Name)
	}
	_, err := cat.SetMediaCandidate(ctx, entry.ID, "embedded", catalog.MediaCandidate{
		Kind: "book", Title: title, Icon: "file:" + entry.ID,
		Line1: "文档 · PDF", Line2: strings.TrimSpace(details.Author), Data: data,
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
	candidate := catalog.MediaCandidate{}
	if parsed.Kind == "audio" {
		candidate.Kind = "audio"
		candidate.Title = fileStem(entry.Name)
		candidate.Line1 = "音乐"
		if music, ok := raw["music"].(map[string]any); ok {
			if title, ok := music["title"].(string); ok && strings.TrimSpace(title) != "" {
				candidate.Title = strings.TrimSpace(title)
			}
			artist, _ := music["artist"].(string)
			album, _ := music["album"].(string)
			candidate.Line2 = strings.TrimSpace(artist)
			candidate.Line3 = strings.TrimSpace(album)
			if music["hasAlbumArt"] == true {
				candidate.Icon = "file:" + entry.ID
			}
		}
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
