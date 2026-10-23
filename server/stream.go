package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
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

// ffmpegArgs builds the input and output options for remuxing or, with
// transcode, re-encoding a file. Codecs browsers decode are copied (cheap
// even for 8K); everything else becomes H.264 (on the GPU when available)
// and AAC.
func (s *Server) ffmpegArgs(f store.File, m media.Meta, transcode bool) (in, out []string) {
	if f.Kind == "audio" || m.Video == "" {
		out = append(out, "-map", "0:a:0", "-vn")
	} else {
		out = append(out, "-map", "0:v:0", "-map", "0:a:0?")
		// Keyframes at segment boundaries keep HLS segments even.
		keyframes := []string{"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds)}
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
			out = append(append(out, "-c:v", "h264_vaapi", "-qp", "24"), keyframes...)
		case s.vaapi != "":
			// Decode in software (old codecs, 10-bit), encode on the GPU.
			in = append(in, "-vaapi_device", s.vaapi)
			out = append(append(out, "-vf", "scale=-2:'min(1080,ih)',format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "24"), keyframes...)
		default:
			out = append(append(out, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
				"-vf", "scale=-2:'min(1080,ih)'"), keyframes...)
		}
	}
	if !transcode && m.Audio == "aac" {
		out = append(out, "-c:a", "copy")
	} else {
		out = append(out, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	return in, out
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
