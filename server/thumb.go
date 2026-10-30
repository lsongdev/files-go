package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/ffmpeg"
	"github.com/lsongdev/files-go/pkg/source"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/tmdb-go"
)

const thumbSize = 480

var (
	errNoThumb = errors.New("no thumbnail")
	errRetry   = errors.New("temporary failure") // not cached
)

// Thumbs renders thumbnails lazily on first request and caches them on
// disk. Failures have a bounded negative-cache lifetime.
type Thumbs struct {
	dir      string
	ffmpeg   string
	ffprobe  string
	pdftoppm string
	store    *store.Store
	scanner  *store.Scanner
	tmdb     *media.TMDB
	slots    chan struct{}

	lifetime                         context.Context
	work                             jobs
	imageSlots, videoSlots, pdfSlots chan struct{}
}

func NewThumbs(ctx context.Context, cfg *config.Config, st *store.Store, scanner *store.Scanner, client *media.TMDB) *Thumbs {
	pdftoppm, _ := exec.LookPath("pdftoppm")
	probe := cfg.FFprobe
	if probe == "" {
		probe = "ffprobe"
	}
	return &Thumbs{dir: filepath.Join(cfg.Data, "cache"), ffmpeg: cfg.FFmpeg, ffprobe: probe, pdftoppm: pdftoppm, store: st,
		scanner: scanner, tmdb: client, slots: make(chan struct{}, 4), lifetime: ctx, imageSlots: make(chan struct{}, 1), videoSlots: make(chan struct{}, 2), pdfSlots: make(chan struct{}, 1)}
}

var goImage = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "bmp": true, "tif": true, "tiff": true}

// Has reports whether a thumbnail can be produced for f.
func (t *Thumbs) Has(f store.File, m media.Meta) bool {
	switch {
	case m.Poster != "":
		return m.Poster != "self" || f.Kind != "document" || t.pdftoppm != ""
	case f.Kind == "image":
		return goImage[media.Ext(f.Name)] || media.Ext(f.Name) == "heic"
	}
	return f.Kind == "video" && m.Video != ""
}

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	m := meta(f)
	ref, size := m.Poster, "w500"
	if strings.HasSuffix(r.URL.Path, "/backdrop") {
		ref, size = m.Backdrop, "w1280"
	} else if ref == "" && s.thumbs.Has(f, m) {
		ref = "self"
	}
	name, err := s.thumbs.Get(r.Context(), f, m, ref, size)
	if errors.Is(err, errNoThumb) {
		w.Header().Set("Cache-Control", "max-age=300")
		writeError(w, http.StatusNotFound, "no thumbnail")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, name)
}

// Get returns the cached image file for an artwork reference of f.
func (t *Thumbs) Get(ctx context.Context, f store.File, m media.Meta, ref, size string) (string, error) {
	if ref == "" {
		return "", errNoThumb
	}
	if id, ok := strings.CutPrefix(ref, "file:"); ok {
		n, _ := strconv.ParseInt(id, 10, 64)
		other, err := t.store.Get(ctx, n)
		if err != nil || (other.Kind != "image" && other.Kind != "video" && other.Kind != "audio") {
			return "", errNoThumb
		}
		return t.Get(ctx, other, meta(other), "self", size)
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("v2:%s\x00%s\x00%d\x00%d\x00%s\x00%s", f.Root, f.Path, f.MTimeNS, f.Size, ref, size)))
	if p, ok := strings.CutPrefix(ref, "tmdb:"); ok {
		sum = sha1.Sum([]byte(size + p)) // shared by every file with this artwork
	}
	key := hex.EncodeToString(sum[:])
	name := filepath.Join(t.dir, key[:2], key+".jpg")
	if info, err := os.Stat(name); err == nil && info.Size() > 0 {
		return name, nil
	}
	err := t.work.do(ctx, t.lifetime, key, time.Minute, func(ctx context.Context) error {
		if info, err := os.Stat(name); err == nil && info.Size() > 0 {
			return nil
		}
		var failure struct{ Until int64 }
		if b, err := os.ReadFile(name + ".err"); err == nil && json.Unmarshal(b, &failure) == nil && time.Now().UnixMilli() < failure.Until {
			return errNoThumb
		}
		release, err := slot(ctx, t.slots)
		if err != nil {
			return err
		}
		defer release()
		data, err := t.render(ctx, f, m, ref, size)
		if err == nil && len(data) == 0 {
			err = errNoThumb
		}
		if err != nil {
			log.Printf("thumbnail %s/%s: %v", f.Root, f.Path, err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			ttl := time.Minute
			if errors.Is(err, errNoThumb) || errors.Is(err, source.ErrLimit) {
				ttl = 24 * time.Hour
			}
			failure.Until = time.Now().Add(ttl).UnixMilli()
			b, _ := json.Marshal(failure)
			if e := source.WriteFile(name+".err", b); e != nil {
				return e
			}
			return errNoThumb
		}
		if len(data) == 0 {
			return errNoThumb
		}
		if err := writeFile(name, data); err != nil {
			return err
		}
		os.Remove(name + ".err")
		return nil
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

func (t *Thumbs) render(ctx context.Context, f store.File, m media.Meta, ref, size string) ([]byte, error) {
	if remote, ok := strings.CutPrefix(ref, "theporndb:"); ok {
		u, err := url.Parse(remote)
		if err != nil || u.Scheme != "https" || (u.Host != "thumb.theporndb.net" && u.Host != "cdn.theporndb.net") {
			return nil, errNoThumb
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errRetry, err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%w: artwork HTTP %d", errRetry, res.StatusCode)
		}
		data, err := source.ReadAll(res.Body, 10<<20)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errRetry, err)
		}
		return data, nil
	}

	if p, ok := strings.CutPrefix(ref, "tmdb:"); ok {
		if t.tmdb == nil {
			return nil, errNoThumb
		}
		body, err := t.tmdb.Image(ctx, size, p)
		if err != nil {
			if tmdb.IsNotFound(err) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", errRetry, err)
		}
		defer body.Close()
		return source.ReadAll(body, 10<<20)
	}
	src, err := t.scanner.Open(ctx, f)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if entry, ok := strings.CutPrefix(ref, "zip:"); ok {
		release, e := slot(ctx, t.imageSlots)
		if e != nil {
			return nil, e
		}
		defer release()
		info, err := src.Stat()
		if err != nil {
			return nil, err
		}
		z, err := zip.NewReader(src, info.Size())
		if err != nil {
			return nil, err
		}
		for _, file := range z.File {
			if file.Name == entry {
				r, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				data, err := source.ReadAll(r, 32<<20)
				if err != nil {
					return nil, err
				}
				return scale(bytes.NewReader(data), "")
			}
		}
		return nil, fmt.Errorf("%s not in archive", entry)
	}
	if f.Kind == "image" {
		release, e := slot(ctx, t.imageSlots)
		if e != nil {
			return nil, e
		}
		defer release()
		info, e := src.Stat()
		if e != nil {
			return nil, e
		}
		if info.Size() > 128<<20 {
			return nil, source.ErrLimit
		}
		if !goImage[media.Ext(f.Name)] {
			data, e := source.Output(ffmpeg.Command(ctx, t.ffprobe, src, "-v", "error", "-max_alloc", "67108864", "-show_entries", "stream=width,height", "-of", "json", source.Input), 1<<20)
			if e != nil {
				return nil, e
			}
			var probe struct{ Streams []struct{ Width, Height int } }
			if e := json.Unmarshal(data, &probe); e != nil {
				return nil, e
			}
			if len(probe.Streams) == 0 {
				return nil, errNoThumb
			}
			w, h := probe.Streams[0].Width, probe.Streams[0].Height
			if w <= 0 || h <= 0 || int64(w) > 32_000_000/int64(h) {
				return nil, source.ErrLimit
			}
		}
	}
	switch {
	case f.Kind == "image" && goImage[media.Ext(f.Name)]:
		file := src
		if data, err := scale(file, media.Ext(f.Name)); err == nil {
			return data, nil
		} else if errors.Is(err, source.ErrLimit) {
			return nil, err
		}
		return t.frame(ctx, src, -1)
	case f.Kind == "document":
		return t.pdf(ctx, src)
	case f.Kind == "video":
		at := 5.0
		if m.Duration > 0 {
			at = min(m.Duration*0.1, 120)
		}
		return t.frame(ctx, src, at)
	default: // embedded cover art, HEIC
		return t.frame(ctx, src, -1)
	}
}

// frame extracts one scaled frame with ffmpeg; at < 0 means the first frame.
func (t *Thumbs) frame(ctx context.Context, src *os.File, at float64) ([]byte, error) {
	args := []string{"-v", "error", "-nostdin", "-max_alloc", "67108864"}
	if at >= 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 2, 64))
	}
	args = append(args, "-threads", "1", "-i", source.Input, "-map", "0:v:0", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", thumbSize, thumbSize),
		"-f", "image2", "-c:v", "mjpeg", "-q:v", "4", "pipe:1")
	release, err := slot(ctx, t.videoSlots)
	if err != nil {
		return nil, err
	}
	cmd := ffmpeg.Command(ctx, t.ffmpeg, src, args...)
	data, err := source.Output(cmd, 5<<20)
	release()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 && at > 0 {
		return t.frame(ctx, src, 0)
	}
	return data, nil
}

func (t *Thumbs) pdf(ctx context.Context, src *os.File) ([]byte, error) {
	if t.pdftoppm == "" {
		return nil, errNoThumb
	}
	release, err := slot(ctx, t.pdfSlots)
	if err != nil {
		return nil, err
	}
	defer release()
	return source.Output(source.Command(ctx, t.pdftoppm, src, "-f", "1", "-l", "1", "-singlefile", "-scale-to", strconv.Itoa(thumbSize), "-jpeg", source.Input), 5<<20)
}

// scale decodes an image, applies EXIF orientation and fits it into the
// thumbnail box.
func scale(r io.ReadSeeker, ext string) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > 32_000_000/int64(cfg.Height) {
		return nil, source.ErrLimit
	}
	orientation := 1
	if ext == "jpg" || ext == "jpeg" || ext == "" {
		r.Seek(0, io.SeekStart)
		if x, err := exif.Decode(r); err == nil {
			if tag, err := x.Get(exif.Orientation); err == nil {
				orientation, _ = tag.Int(0)
			}
		}
	}
	r.Seek(0, io.SeekStart)
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > thumbSize || h > thumbSize {
		if w >= h {
			w, h = thumbSize, max(1, h*thumbSize/w)
		} else {
			w, h = max(1, w*thumbSize/h), thumbSize
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src) // flatten transparency
	draw.BiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, orient(dst, orientation), &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// orient applies an EXIF orientation (2-8) to an image.
func orient(src *image.RGBA, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	transpose := orientation >= 5
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if transpose {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.SetRGBA(dx, dy, src.RGBAAt(x, y))
		}
	}
	return dst
}

func writeFile(name string, data []byte) error { return source.WriteFile(name, data) }
