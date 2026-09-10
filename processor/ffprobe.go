package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

var ErrProcessOutputTooLarge = errors.New("external process output exceeded limit")

type FFProbe struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
	binary   string
	timeout  time.Duration
}

func NewFFProbe(catalog *catalog.Catalog, storages *storage.Registry, binary string, timeout time.Duration) *FFProbe {
	if binary == "" {
		binary = "ffprobe"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &FFProbe{catalog: catalog, storages: storages, binary: binary, timeout: timeout}
}

func (p *FFProbe) Name() string { return "ffprobe" }

func (p *FFProbe) Match(entry model.Entry) bool {
	if isMetadataSidecar(entry) {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "flv", "wmv",
		"mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "wma", "aiff", "ape":
		return true
	default:
		return false
	}
}

func (p *FFProbe) Process(ctx context.Context, entry model.Entry) error {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	native, ok := backend.(storage.NativePather)
	if !ok {
		return storage.ErrUnsupported
	}
	filename, err := native.NativePath(ctx, entry.Path)
	if err != nil {
		return err
	}
	processCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(processCtx, p.binary,
		"-v", "error", "-show_format", "-show_streams", "-show_chapters", "-of", "json", filename)
	stdout := &limitedBuffer{limit: 4 << 20}
	stderr := &limitedBuffer{limit: 64 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if processCtx.Err() != nil {
			return fmt.Errorf("ffprobe timeout: %w", processCtx.Err())
		}
		if stdout.exceeded || stderr.exceeded {
			return ErrProcessOutputTooLarge
		}
		return fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return ErrProcessOutputTooLarge
	}
	media, err := parseFFProbe(entry.ID, stdout.Bytes())
	if err != nil {
		return err
	}
	return p.catalog.UpsertMediaFile(ctx, media)
}

type ffprobeStream struct {
	CodecType   string `json:"codec_type"`
	CodecName   string `json:"codec_name"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Duration    string `json:"duration"`
	Disposition struct {
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
	Tags map[string]string `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		Bitrate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

func parseFFProbe(entryID string, data []byte) (model.MediaFile, error) {
	var output ffprobeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return model.MediaFile{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	metadata := append(json.RawMessage(nil), data...)
	item := model.MediaFile{EntryID: entryID, Container: output.Format.FormatName, Metadata: metadata}
	hasAlbumArt := false
	for _, stream := range output.Streams {
		if stream.Disposition.AttachedPic != 0 {
			hasAlbumArt = true
			continue
		}
		switch stream.CodecType {
		case "video":
			if item.VideoCodec == "" {
				item.Kind = "video"
				item.VideoCodec = stream.CodecName
				if stream.Width > 0 {
					width := stream.Width
					item.Width = &width
				}
				if stream.Height > 0 {
					height := stream.Height
					item.Height = &height
				}
			}
		case "audio":
			if item.AudioCodec == "" {
				item.AudioCodec = stream.CodecName
				if item.Kind == "" {
					item.Kind = "audio"
				}
			}
		}
	}
	if item.Kind == "" {
		return model.MediaFile{}, errors.New("ffprobe found no audio or video streams")
	}
	if seconds, err := strconv.ParseFloat(output.Format.Duration, 64); err == nil && seconds >= 0 {
		duration := int64(seconds*1000 + 0.5)
		item.DurationMS = &duration
	}
	if value, err := strconv.ParseInt(output.Format.Bitrate, 10, 64); err == nil && value >= 0 {
		item.Bitrate = &value
	}
	if item.Kind == "audio" {
		music := normalizedMusicMetadata(output.Format.Tags, output.Streams, hasAlbumArt)
		var document map[string]any
		if err := json.Unmarshal(data, &document); err == nil {
			document["music"] = music
			if encoded, err := json.Marshal(document); err == nil {
				item.Metadata = encoded
			}
		}
	}
	return item, nil
}

func normalizedMusicMetadata(formatTags map[string]string, streams []ffprobeStream, hasAlbumArt bool) map[string]any {
	tags := make(map[string]string)
	for key, value := range formatTags {
		tags[strings.ToLower(key)] = strings.TrimSpace(value)
	}
	for _, stream := range streams {
		if stream.CodecType != "audio" {
			continue
		}
		for key, value := range stream.Tags {
			lower := strings.ToLower(key)
			if tags[lower] == "" {
				tags[lower] = strings.TrimSpace(value)
			}
		}
	}
	result := map[string]any{"hasAlbumArt": hasAlbumArt}
	for _, field := range []string{"title", "album", "artist", "album_artist", "composer", "genre", "date", "track", "disc"} {
		if value := tags[field]; value != "" {
			result[field] = value
		}
	}
	return result
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Buffer.Len()+len(data) > b.limit {
		remaining := b.limit - b.Buffer.Len()
		if remaining > 0 {
			_, _ = b.Buffer.Write(data[:remaining])
		}
		b.exceeded = true
		return max(remaining, 0), ErrProcessOutputTooLarge
	}
	return b.Buffer.Write(data)
}
