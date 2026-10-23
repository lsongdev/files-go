package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

// directVideo reports whether browsers can play the file as-is.
func directVideo(f store.File, m media.Meta) bool {
	switch media.Ext(f.Name) {
	case "mp4", "m4v", "mov":
		return m.Video == "h264" && (m.Audio == "" || m.Audio == "aac" || m.Audio == "mp3")
	case "webm":
		return (m.Video == "vp8" || m.Video == "vp9" || m.Video == "av1") && (m.Audio == "" || m.Audio == "opus" || m.Audio == "vorbis")
	}
	return false
}

// stream remuxes or transcodes a media file with ffmpeg into fragmented MP4
// written straight to the response. Seeking restarts the stream at ?t=sec.
// Codecs browsers decode are copied (cheap even for 8K); others, or all with
// ?transcode=1 (the player's fallback), become 1080p H.264 and AAC.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if f.Kind != "video" && f.Kind != "audio" {
		writeError(w, http.StatusBadRequest, "not a media file")
		return
	}
	src, err := s.scanner.Abs(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		writeError(w, http.StatusServiceUnavailable, "too many concurrent streams")
		return
	}
	m := meta(f)
	transcode := r.URL.Query().Get("transcode") == "1"
	start, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	var in, out []string
	contentType := "video/mp4"
	if f.Kind == "audio" || m.Video == "" {
		contentType = "audio/mp4"
		out = append(out, "-map", "0:a:0", "-vn")
	} else {
		out = append(out, "-map", "0:v:0", "-map", "0:a:0?")
		switch {
		case !transcode && (m.Video == "h264" || m.Video == "vp9" || m.Video == "av1"):
			out = append(out, "-c:v", "copy")
		case !transcode && m.Video == "hevc":
			out = append(out, "-c:v", "copy", "-tag:v", "hvc1")
		case s.vaapi != "" && gpuDecodes(m):
			// Decode and encode on the GPU at the original size. Scaling on
			// the GPU (scale_vaapi) is broken with ffmpeg 4.4 and current
			// Intel drivers, and H.264 handles 4K fine.
			in = append(in, "-vaapi_device", s.vaapi, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi")
			out = append(out, "-c:v", "h264_vaapi", "-qp", "24", "-g", "48")
		case s.vaapi != "":
			// Decode in software (old codecs, 10-bit), encode on the GPU.
			in = append(in, "-vaapi_device", s.vaapi)
			out = append(out, "-vf", "scale=-2:'min(1080,ih)',format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "24", "-g", "48")
		default:
			out = append(out, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
				"-g", "48", "-vf", "scale=-2:'min(1080,ih)'")
		}
	}
	if !transcode && (m.Audio == "aac" || m.Audio == "mp3" || m.Audio == "opus") {
		out = append(out, "-c:a", "copy")
	} else {
		out = append(out, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	args := append([]string{"-v", "error", "-nostdin"}, in...)
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(append(args, "-i", src), out...)
	args = append(args, "-strict", "-2", "-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "pipe:1")

	cmd := exec.CommandContext(r.Context(), s.cfg.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(w, err)
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		s.fail(w, fmt.Errorf("start ffmpeg: %w", err))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, copyErr := io.Copy(w, stdout)
	if err := cmd.Wait(); err != nil && r.Context().Err() == nil {
		log.Printf("stream %s/%s: %v: %s (copy: %v)", f.Root, f.Path, err, strings.TrimSpace(stderr.String()), copyErr)
	}
}

// gpuDecodes reports codecs Intel Quick Sync decodes into surfaces the
// H.264 encoder accepts: 8-bit and at most 4096 pixels wide.
func gpuDecodes(m media.Meta) bool {
	switch m.Video {
	case "h264", "hevc", "vp9", "vp8", "mpeg2video", "vc1":
		return m.Depth <= 8 && m.Width <= 4096 && m.Height <= 4096
	}
	return false
}

// detectVAAPI returns the render device when ffmpeg can encode H.264 on it.
func detectVAAPI(ffmpeg, mode, device string) string {
	if mode == "none" {
		return ""
	}
	if _, err := os.Stat(device); err != nil {
		if mode == "vaapi" {
			log.Printf("hardware transcoding unavailable: %v", err)
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-vaapi_device", device,
		"-f", "lavfi", "-i", "color=black:s=320x240:d=0.2", "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-f", "null", "-").CombinedOutput()
	if err != nil {
		log.Printf("hardware transcoding unavailable: %v: %s", err, strings.TrimSpace(string(out)))
		return ""
	}
	log.Printf("hardware transcoding: VAAPI on %s", device)
	return device
}
