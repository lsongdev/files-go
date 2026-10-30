package hls

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lsongdev/files-go/pkg/ffmpeg"
	"github.com/lsongdev/files-go/pkg/source"
	"math"
	"strconv"
	"strings"
)

type segment struct {
	start, duration float64
	frames          int // exact packet count when copying video, including B frames
}

// timeline probes metadata, and packet headers when copying video. It does not
// decode or encode media. Copy boundaries follow the source's keyframes;
// re-encoded media can start at any boundary.
func (m *Manager) timeline(ctx context.Context, src Source) ([]segment, error) {
	input, err := src.open(ctx)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	data, err := source.Output(ffmpeg.Command(ctx, m.cfg.FFprobe, input, "-v", "error", "-show_entries", "format=duration,start_time", "-of", "json", source.Input), 1<<20)
	if err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}
	var info struct {
		Format struct {
			Duration string `json:"duration"`
			Start    string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}
	duration, _ := strconv.ParseFloat(info.Format.Duration, 64)
	if !finite(duration) || duration <= 0 {
		return nil, fmt.Errorf("media has no finite positive duration")
	}
	if duration/m.cfg.SegmentDuration.Seconds() > float64(m.cfg.MaxSegments) {
		return nil, fmt.Errorf("timeline exceeds %d segment limit", m.cfg.MaxSegments)
	}
	start, _ := strconv.ParseFloat(info.Format.Start, 64)
	if !finite(start) {
		start = 0
	}
	boundaries := []segment{{}}
	if src.CopyVideo {
		cmd := ffmpeg.Command(ctx, m.cfg.FFprobe, input, "-v", "error", "-select_streams", "v:0", "-show_packets", "-show_entries", "packet=pts_time,flags", "-of", "compact=p=0", source.Input)
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		var stderr limitedBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		scan := bufio.NewScanner(pipe)
		frames := 0
		for scan.Scan() {
			var pts, flags string
			for _, field := range strings.Split(scan.Text(), "|") {
				k, v, _ := strings.Cut(field, "=")
				switch k {
				case "pts_time":
					pts = v
				case "flags":
					flags = v
				}
			}
			if pts == "" { // e.g. packet side data
				continue
			}
			t, err := strconv.ParseFloat(pts, 64)
			if err != nil || !finite(t) {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return nil, fmt.Errorf("cannot copy video with missing packet timestamps")
			}
			t -= start
			last := &boundaries[len(boundaries)-1]
			if strings.Contains(flags, "K") && t >= last.start+m.cfg.SegmentDuration.Seconds() && t < duration {
				last.duration, last.frames = t-last.start, frames-last.frames
				if len(boundaries) >= m.cfg.MaxSegments {
					cmd.Process.Kill()
					cmd.Wait()
					return nil, fmt.Errorf("timeline segment limit exceeded")
				}
				boundaries = append(boundaries, segment{start: t, frames: frames})
			}
			frames++
		}
		if err := scan.Err(); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, err
		}
		if err := cmd.Wait(); err != nil {
			return nil, fmt.Errorf("probe keyframes: %w: %s", err, stderr.String())
		}
		if frames == 0 {
			return nil, fmt.Errorf("no video packets to copy")
		}
		last := &boundaries[len(boundaries)-1]
		last.frames = frames - last.frames
	} else {
		for at := m.cfg.SegmentDuration.Seconds(); at < duration; at += m.cfg.SegmentDuration.Seconds() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			boundaries[len(boundaries)-1].duration = at - boundaries[len(boundaries)-1].start
			boundaries = append(boundaries, segment{start: at})
		}
	}
	boundaries[len(boundaries)-1].duration = duration - boundaries[len(boundaries)-1].start
	return boundaries, nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func playlist(segments []segment) []byte {
	var b strings.Builder
	target := 1
	for _, seg := range segments {
		target = max(target, int(math.Ceil(seg.duration)))
	}
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MEDIA-SEQUENCE:0\n", target)
	for i, seg := range segments {
		// Each fragment is an independent encode/remux. Its own init section
		// and discontinuity allow arbitrary request order and local timestamps.
		if i > 0 {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"init-%05d.mp4\"\n#EXTINF:%.6f,\nseg-%05d.m4s\n", i, seg.duration, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}
