// Package ffmpeg adapts rooted file handles to self-contained media inputs.
package ffmpeg

import (
	"context"
	"github.com/lsongdev/files-go/pkg/source"
	"os"
	"os/exec"
)

// Only self-contained containers are accepted. In particular, an input renamed
// to .mp4 must not become an HLS/concat/DASH manifest that reads other paths or
// URLs after the top-level descriptor has been validated. MOV external track
// references remain disabled by libavformat's default enable_drefs=false.
const formats = "mov,matroska,webm,avi,asf,flv,rm,mpeg,mpegts,mpegvideo,h264,hevc,mp3,flac,wav,aac,ogg,aiff,ape,dsf,iff,amr,ass,srt,webvtt,jpeg_pipe,mjpeg,png_pipe,gif,webp_pipe,bmp_pipe,tiff_pipe,psd_pipe,image2,image2pipe,ico,heif,avif"

func Command(ctx context.Context, bin string, input *os.File, args ...string) *exec.Cmd {
	opts := []string{"-protocol_whitelist", "file,pipe", "-format_whitelist", formats}
	return source.Command(ctx, bin, input, append(opts, args...)...)
}
