package hls

import (
	"context"
	"fmt"
	"github.com/lsongdev/files-go/pkg/ffmpeg"
	"github.com/lsongdev/files-go/pkg/source"
	"os"
	"path/filepath"
	"strconv"
)

func seconds(n float64) string { return strconv.FormatFloat(n, 'f', 6, 64) }

// render writes exactly one requested media fragment and its initialization
// section. No adjacent fragments or background transcoding jobs are started.
func (m *Manager) render(ctx context.Context, src Source, seg segment, dir string) error {
	args := append([]string{"-v", "error", "-nostdin"}, src.InputArgs...)
	args = append(args, "-ss", seconds(seg.start), "-i", source.Input, "-t", seconds(seg.duration))
	args = append(args, src.OutputArgs...)
	if src.CopyVideo {
		// Drop packets beyond this GOP without -frames:v: that option ends
		// all output streams early, starving slower audio encoders such as DTS
		// to AAC. A bitstream filter bounds video while audio runs through -t.
		args = append(args, "-bsf:v", "noise=drop='gte(n,"+strconv.Itoa(seg.frames)+")'")
	}
	args = append(args, "-strict", "-2", "-f", "hls", "-hls_time", seconds(seg.duration+60),
		"-hls_playlist_type", "vod", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "segment-%d.m4s"), filepath.Join(dir, "index.m3u8"))
	input, err := src.open(ctx)
	if err != nil {
		return err
	}
	defer input.Close()
	cmd := ffmpeg.Command(ctx, m.cfg.FFmpeg, input, args...)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("generate HLS fragment: %w: %s", err, stderr.String())
	}
	if err := os.Rename(filepath.Join(dir, "segment-0.m4s"), filepath.Join(dir, "segment.m4s")); err != nil {
		return fmt.Errorf("publish HLS fragment: %w", err)
	}
	for _, name := range []string{"init.mp4", "segment.m4s"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("generate %s: %w", name, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("generated empty %s", name)
		}
	}
	return nil
}

// Keep diagnostics bounded even if ffmpeg repeatedly reports damaged packets.
type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 8192
	b.data = append(b.data, p...)
	if len(b.data) > limit {
		b.data = b.data[len(b.data)-limit:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.data) }
