package hls

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lsongdev/files-go/pkg/source"
)

// Timeline entries are independent of playback tokens and fragment caches.
// A versioned key includes actual source stat, rendition, segment policy and
// executable identities. Corrupt/old entries are regenerated, not trusted.
type timelineEntry struct {
	Checksum string
	Version  int
	Segments []cachedSegment
}
type cachedSegment struct {
	Start, Duration float64
	Frames          int
}

func toolID(bin string) string {
	p, e := exec.LookPath(bin)
	if e != nil {
		return bin
	}
	i, e := os.Stat(p)
	if e != nil {
		return p
	}
	return fmt.Sprintf("%s:%d:%d", p, i.Size(), i.ModTime().UnixNano())
}
func (m *Manager) timelineKey(src Source) string {
	b, _ := json.Marshal([]any{2, src, src.size, src.mtime.UnixNano(), m.cfg.SegmentDuration.Nanoseconds(), toolID(m.cfg.FFprobe), toolID(m.cfg.FFmpeg)})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (m *Manager) loadTimeline(src Source) ([]segment, bool) {
	m.timelineMu.Lock()
	defer m.timelineMu.Unlock()
	name := filepath.Join(m.cfg.CacheDir, "timelines", m.timelineKey(src)+".json")
	if f, e := os.Open(name); e == nil {
		data, e := source.ReadAll(f, 16<<20)
		f.Close()
		var entry timelineEntry
		if e == nil && json.Unmarshal(data, &entry) == nil && entry.Version == 2 && len(entry.Segments) > 0 && len(entry.Segments) <= m.cfg.MaxSegments && entry.Checksum == timelineChecksum(entry.Segments) {
			result := make([]segment, 0, len(entry.Segments))
			at := 0.
			valid := true
			for _, s := range entry.Segments {
				if !finite(s.Start) || !finite(s.Duration) || s.Duration <= 0 || math.Abs(s.Start-at) > 1e-6 || src.CopyVideo && s.Frames <= 0 {
					valid = false
					break
				}
				result = append(result, segment{start: s.Start, duration: s.Duration, frames: s.Frames})
				at = s.Start + s.Duration
			}
			if valid && finite(at) {
				os.Chtimes(name, time.Now(), time.Now())
				return result, true
			}
		}
	}
	return nil, false
}

func (m *Manager) preparedTimeline(ctx context.Context, src Source) ([]segment, error) {
	if segments, ok := m.loadTimeline(src); ok {
		return segments, nil
	}
	name := filepath.Join(m.cfg.CacheDir, "timelines", m.timelineKey(src)+".json")
	result, err := m.timeline(ctx, src)
	if err != nil {
		return nil, err
	}
	if err := unchanged(ctx, src.File, src.size, src.mtime); err != nil {
		return nil, err
	}
	entry := timelineEntry{Version: 2}
	for _, s := range result {
		entry.Segments = append(entry.Segments, cachedSegment{s.start, s.duration, s.frames})
	}
	entry.Checksum = timelineChecksum(entry.Segments)
	b, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	if len(b) <= 16<<20 {
		if err := source.WriteFile(name, b); err != nil {
			return nil, err
		}
		m.pruneTimelines()
	}
	return result, nil
}
func (m *Manager) pruneTimelines() {
	m.timelineMu.Lock()
	defer m.timelineMu.Unlock()
	dir := filepath.Join(m.cfg.CacheDir, "timelines")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		info os.FileInfo
	}
	var files []file
	var total int64
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cache-") {
			if i, err := e.Info(); err == nil && time.Since(i.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dir, e.Name()))
			}
			continue
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		i, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{e.Name(), i})
		total += i.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	remaining := len(files)
	for _, f := range files {
		if total <= 32<<20 && remaining <= 1024 && time.Since(f.info.ModTime()) <= 30*24*time.Hour {
			break
		}
		if os.Remove(filepath.Join(dir, f.name)) == nil {
			total -= f.info.Size()
			remaining--
		}
	}
}

func timelineChecksum(s []cachedSegment) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
