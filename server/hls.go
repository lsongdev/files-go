package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/hls"
	"github.com/lsongdev/files-go/pkg/source"
	"os"
)

// play publishes a complete timeline without producing any media fragments.
// Codec/hardware policy stays here; the reusable HLS package handles delivery.
func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if f.Kind != "video" && f.Kind != "audio" {
		writeError(w, http.StatusBadRequest, "not a media file")
		return
	}
	m := meta(f)
	if f.Kind == "video" && m.Video == "" || f.Kind == "audio" && m.Audio == "" {
		bin := s.cfg.FFprobe
		if bin == "" {
			bin = "ffprobe"
		}
		probe := media.Probe{Bin: bin, Open: s.scanner.Open, Store: s.store}
		if err := probe.Enrich(r.Context(), &f, &m); err != nil {
			s.fail(w, err)
			return
		}
	}
	force := r.URL.Query().Get("transcode") == "1"
	in, out := s.ffmpegArgs(f, m, force)
	copyVideo := f.Kind == "video" && !force && (m.Video == "h264" || m.Video == "hevc" || m.Video == "vp9" || m.Video == "av1")
	root, _ := s.scanner.Root(f.Root)
	play, err := s.hls.Open(r.Context(), hls.Source{File: source.File{Key: root.Path + ":" + f.Path, Open: func(ctx context.Context) (*os.File, error) { return s.scanner.Open(ctx, f) }}, CopyVideo: copyVideo, InputArgs: in, OutputArgs: out})
	if err != nil {
		s.hlsError(w, err)
		return
	}
	writeJSON(w, map[string]any{"url": "/api/hls/" + play.Key + "/index.m3u8", "duration": play.Duration})
}

var segmentName = regexp.MustCompile(`^(init|seg)-(\d{5,})\.(mp4|m4s)$`)

func (s *Server) segment(w http.ResponseWriter, r *http.Request) {
	key, name := r.PathValue("key"), r.PathValue("name")
	if name == "index.m3u8" {
		data, err := s.hls.Playlist(r.Context(), key)
		if err != nil {
			s.hlsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
		return
	}
	match := segmentName.FindStringSubmatch(name)
	if match == nil || match[1] == "init" && match[3] != "mp4" || match[1] == "seg" && match[3] != "m4s" {
		writeError(w, http.StatusNotFound, "unknown HLS resource")
		return
	}
	index, err := strconv.Atoi(match[2])
	if err != nil {
		writeError(w, http.StatusNotFound, "invalid fragment")
		return
	}
	file, err := s.hls.Segment(r.Context(), key, index, match[1] == "init")
	if err != nil {
		s.hlsError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=1800")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (s *Server) releasePlayback(w http.ResponseWriter, r *http.Request) {
	s.hls.Release(r.PathValue("key"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) hlsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return // the viewer stopped waiting; no response remains to deliver
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "media generation timed out")
	case errors.Is(err, hls.ErrNotFound):
		writeError(w, http.StatusNotFound, "playback session ended or fragment not found")
	case errors.Is(err, hls.ErrSourceChanged):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.fail(w, err)
	}
}
