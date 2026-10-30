package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/pkg/ffmpeg"
	"github.com/lsongdev/files-go/pkg/source"
	"github.com/lsongdev/files-go/store"
)

// Opener returns a rooted regular file. Callers own and close the handle.
type Opener func(context.Context, store.File) (*os.File, error)

// Probe reads container, stream and tag information with ffprobe.
type Probe struct {
	Bin   string
	Open  Opener
	Store *store.Store
}

func (p *Probe) Match(f *store.File) bool { return f.Kind == "video" || f.Kind == "audio" }

func (p *Probe) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	input, err := p.Open(ctx, *f)
	if err != nil {
		return err
	}
	defer input.Close()
	if ext := Ext(f.Name); (ext == "ts" || ext == "mts") && !transportStream(input) {
		f.Kind = "code" // TypeScript, not MPEG-TS
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	key := fmt.Sprintf("probe:v2:%d", f.ID)
	var cached struct {
		Size, Modified int64
		Bin            string
		Data           json.RawMessage
	}
	if p.Store != nil {
		if b, ok := p.Store.CacheGet(ctx, key, 30*24*time.Hour); ok && json.Unmarshal(b, &cached) == nil && cached.Size == info.Size() && cached.Modified == info.ModTime().UnixNano() && cached.Bin == p.Bin {
			return parseProbe(cached.Data, f, m)
		}
	}
	cmd := ffmpeg.Command(ctx, p.Bin, input, "-v", "error", "-show_format", "-show_streams", "-of", "json", source.Input)
	data, err := source.Output(cmd, 4<<20)
	if err != nil {
		return err
	}
	if err := parseProbe(data, f, m); err != nil {
		return err
	}
	if p.Store != nil {
		cached.Size, cached.Modified, cached.Bin, cached.Data = info.Size(), info.ModTime().UnixNano(), p.Bin, data
		b, _ := json.Marshal(cached)
		if err := p.Store.CachePut(ctx, key, b); err != nil {
			return err
		}
	}
	return nil
}

type probeOutput struct {
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		PixFmt      string            `json:"pix_fmt"`
		Width       int               `json:"width"`
		Height      int               `json:"height"`
		Channels    int               `json:"channels"`
		SampleRate  string            `json:"sample_rate"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
			Default     int `json:"default"`
			Forced      int `json:"forced"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

func parseProbe(data []byte, f *store.File, m *Meta) error {
	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("decode ffprobe: %w", err)
	}
	m.Container = strings.Split(out.Format.FormatName, ",")[0]
	m.Duration, _ = strconv.ParseFloat(out.Format.Duration, 64)
	if rate, err := strconv.ParseInt(out.Format.BitRate, 10, 64); err == nil && rate > 0 {
		m.Set("Bitrate", fmt.Sprintf("%d kb/s", rate/1000))
	}
	for _, s := range out.Streams {
		switch {
		case s.Disposition.AttachedPic != 0:
			m.Poster = "self"
		case s.CodecType == "video" && m.Video == "":
			m.Video, m.Width, m.Height = s.CodecName, s.Width, s.Height
			if m.Depth = 8; strings.Contains(s.PixFmt, "10") || strings.Contains(s.PixFmt, "12") {
				m.Depth = 10
			}
		case s.CodecType == "audio" && m.Audio == "":
			m.Audio = s.CodecName
			if s.Channels > 0 {
				m.Set("Channels", strconv.Itoa(s.Channels))
			}
			if s.SampleRate != "" {
				m.Set("Sample rate", s.SampleRate+" Hz")
			}
		case s.CodecType == "subtitle":
			// Index counts subtitle streams only, as in ffmpeg's -map 0:s:N.
			m.Subtitles = append(m.Subtitles, Subtitle{Index: len(m.Subtitles), Codec: s.CodecName,
				Lang: s.Tags["language"], Title: s.Tags["title"], Default: s.Disposition.Default != 0, Forced: s.Disposition.Forced != 0})
		}
	}
	switch {
	case m.Video != "":
		f.Kind, m.Kind = "video", "video"
	case m.Audio != "":
		f.Kind, m.Kind = "audio", "audio"
	default:
		return fmt.Errorf("no audio or video stream")
	}
	tags := map[string]string{}
	for k, v := range out.Format.Tags {
		tags[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	if m.Kind == "audio" {
		m.Title, m.Artist, m.Album = tags["title"], tags["artist"], tags["album"]
		m.AlbumArtist = first(tags["album_artist"], tags["albumartist"])
		m.Artist = first(m.Artist, m.AlbumArtist)
		m.Year = leadingYear(tags["date"])
		m.Genres = splitList(tags["genre"])
		m.Set("Track", tags["track"])
		m.Set("Disc", tags["disc"])
		m.Set("Composer", tags["composer"])
	} else if title := tags["title"]; title != "" {
		m.Set("Embedded title", title)
	}
	return nil
}

// transportStream checks for the 0x47 sync byte of 188-byte TS packets or
// 192-byte AVCHD/BDAV packets.
func transportStream(file *os.File) bool {
	defer file.Seek(0, 0)
	buf := make([]byte, 192*3)
	if _, err := io.ReadFull(file, buf); err != nil {
		return false
	}
	return buf[0] == 0x47 && buf[188] == 0x47 && buf[376] == 0x47 ||
		buf[4] == 0x47 && buf[196] == 0x47 && buf[388] == 0x47
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func leadingYear(s string) int {
	if len(s) >= 4 {
		if y, err := strconv.Atoi(s[:4]); err == nil && y > 1800 && y < 2200 {
			return y
		}
	}
	return 0
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '/' || r == ',' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
