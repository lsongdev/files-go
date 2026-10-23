package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

// Track is a subtitle the player can load as WebVTT.
type Track struct {
	ID      string `json:"id"` // s<index> embedded stream, f<file id> external file
	Label   string `json:"label"`
	Lang    string `json:"lang,omitempty"`
	Default bool   `json:"default,omitempty"`
}

var subtitleExt = map[string]bool{"srt": true, "ass": true, "ssa": true, "vtt": true}

// tracks lists a video's embedded text subtitles and external subtitle
// files next to it: "Movie.chs.srt" belongs to "Movie.mkv", and when a
// folder holds a single video every subtitle file in it belongs to it.
func (s *Server) tracks(ctx context.Context, f store.File) ([]Track, error) {
	m := meta(f)
	var tracks []Track
	for _, sub := range m.Subtitles {
		if sub.Text() {
			label := first(sub.Title, languageName(sub.Lang), fmt.Sprintf("字幕 %d", sub.Index+1))
			tracks = append(tracks, Track{ID: "s" + strconv.Itoa(sub.Index), Label: label, Lang: sub.Lang, Default: sub.Default})
		}
	}
	siblings, err := s.store.Children(ctx, f.Parent, 0, 2000)
	if err != nil {
		return nil, err
	}
	videos := 0
	for _, sib := range siblings {
		if sib.Kind == "video" {
			videos++
		}
	}
	stem := strings.ToLower(strings.TrimSuffix(f.Name, filepath.Ext(f.Name)))
	for _, sib := range siblings {
		ext := media.Ext(sib.Name)
		if sib.Dir || !subtitleExt[ext] {
			continue
		}
		name := strings.TrimSuffix(sib.Name, filepath.Ext(sib.Name))
		ok := strings.HasPrefix(strings.ToLower(name), stem)
		if !ok && videos != 1 {
			continue
		}
		rest := name
		if ok {
			rest = name[min(len(stem), len(name)):]
		}
		tag := strings.Trim(rest, " ._-[]()")
		tracks = append(tracks, Track{ID: "f" + strconv.FormatInt(sib.ID, 10), Label: first(languageName(tag), tag, "外挂字幕"), Lang: tag})
	}
	// Prefer Chinese, then whatever the file marks as default.
	best := -1
	for i, t := range tracks {
		if chinese(t) {
			best = i
			break
		}
		if t.Default && best < 0 {
			best = i
		}
	}
	for i := range tracks {
		tracks[i].Default = i == best
	}
	return tracks, nil
}

func (s *Server) subtitles(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	tracks, err := s.tracks(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"items": tracks})
}

// subtitle converts one track to WebVTT with ffmpeg and caches the result.
func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	id := strings.TrimSuffix(r.PathValue("track"), ".vtt")
	tracks, err := s.tracks(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	known := false
	for _, t := range tracks {
		known = known || t.ID == id
	}
	if !known {
		writeError(w, http.StatusNotFound, "subtitle not found")
		return
	}
	src := f
	var args []string
	if fid, ok := strings.CutPrefix(id, "f"); ok {
		n, _ := strconv.ParseInt(fid, 10, 64)
		if src, err = s.store.Get(r.Context(), n); err != nil {
			s.fail(w, err)
			return
		}
	}
	name, err := s.scanner.Abs(src)
	if err != nil {
		s.fail(w, err)
		return
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", src.Root, src.Path, src.MTime, src.Size, id)))
	key := hex.EncodeToString(sum[:])
	cached := filepath.Join(s.cfg.Data, "cache", "subtitles", key[:2], key+".vtt")
	if _, err := os.Stat(cached); err != nil {
		if index, ok := strings.CutPrefix(id, "s"); ok {
			args = []string{"-i", name, "-map", "0:s:" + index}
		} else {
			// Chinese .srt/.ass files are often GBK rather than UTF-8.
			data, err := os.ReadFile(name)
			if err != nil {
				s.fail(w, err)
				return
			}
			if !utf8.Valid(data) && !bytes.HasPrefix(data, []byte{0xff, 0xfe}) && !bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
				args = append(args, "-sub_charenc", "GB18030")
			}
			args = append(args, "-i", name)
		}
		// Embedded tracks need a pass over the whole file; keep going if
		// the client gives up so the next request hits the cache.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		var out, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, s.cfg.FFmpeg, append(append([]string{"-v", "error", "-nostdin"}, args...), "-f", "webvtt", "pipe:1")...)
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			s.fail(w, fmt.Errorf("convert subtitle: %v: %s", err, strings.TrimSpace(stderr.String())))
			return
		}
		if err := writeFile(cached, out.Bytes()); err != nil {
			s.fail(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, cached)
}

func chinese(t Track) bool {
	return strings.ContainsAny(languageName(t.Lang)+t.Label, "中简繁")
}

var languages = map[string]string{
	"chi": "中文", "zho": "中文", "zh": "中文", "chs": "简体中文", "sc": "简体中文", "zh-hans": "简体中文", "zh-cn": "简体中文", "gb": "简体中文",
	"cht": "繁体中文", "tc": "繁体中文", "zh-hant": "繁体中文", "zh-tw": "繁体中文", "big5": "繁体中文",
	"chs&eng": "简英双语", "chs.eng": "简英双语", "cht&eng": "繁英双语",
	"eng": "English", "en": "English", "jpn": "日本語", "ja": "日本語", "kor": "한국어", "ko": "한국어",
	"fre": "Français", "fra": "Français", "ger": "Deutsch", "deu": "Deutsch", "spa": "Español", "rus": "Русский",
}

func languageName(code string) string { return languages[strings.ToLower(code)] }
