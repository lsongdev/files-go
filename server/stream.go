package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

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
// H.264 video is copied; anything else is transcoded to 1080p H.264.
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
	start, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	args := []string{"-v", "error", "-nostdin"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", src)
	contentType := "video/mp4"
	if f.Kind == "audio" || m.Video == "" {
		contentType = "audio/mp4"
		args = append(args, "-map", "0:a:0", "-vn")
	} else {
		args = append(args, "-map", "0:v:0", "-map", "0:a:0?")
		if m.Video == "h264" {
			args = append(args, "-c:v", "copy")
		} else {
			args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
				"-vf", "scale=-2:'min(1080,ih)'")
		}
	}
	if m.Audio == "aac" {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	args = append(args, "-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "pipe:1")

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
