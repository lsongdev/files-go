package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/tmdb-go"
)

const thumbSize = 480

var (
	errNoThumb = errors.New("no thumbnail")
	errRetry   = errors.New("temporary failure") // not cached
)

// Thumbs renders thumbnails lazily on first request and caches them on
// disk. A failed attempt is cached as an empty file so it is not retried.
type Thumbs struct {
	dir      string
	ffmpeg   string
	pdftoppm string
	store    *store.Store
	scanner  *store.Scanner
	tmdb     *media.TMDB
	slots    chan struct{}

	mu       sync.Mutex
	inflight map[string]*sync.WaitGroup
}

func NewThumbs(cfg *config.Config, st *store.Store, scanner *store.Scanner, client *media.TMDB) *Thumbs {
	pdftoppm, _ := exec.LookPath("pdftoppm")
	return &Thumbs{dir: filepath.Join(cfg.Data, "cache"), ffmpeg: cfg.FFmpeg, pdftoppm: pdftoppm, store: st,
		scanner: scanner, tmdb: client, slots: make(chan struct{}, runtime.NumCPU()), inflight: map[string]*sync.WaitGroup{}}
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
		if err != nil || other.Kind != "image" {
			return "", errNoThumb
		}
		return t.Get(ctx, other, media.Meta{}, "self", size)
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s\x00%s", f.Root, f.Path, f.MTime, f.Size, ref, size)))
	if p, ok := strings.CutPrefix(ref, "tmdb:"); ok {
		sum = sha1.Sum([]byte(size + p)) // shared by every file with this artwork
	}
	key := hex.EncodeToString(sum[:])
	name := filepath.Join(t.dir, key[:2], key+".jpg")
	if info, err := os.Stat(name); err == nil {
		if info.Size() == 0 {
			return "", errNoThumb
		}
		return name, nil
	}
	// Collapse concurrent requests for the same thumbnail.
	t.mu.Lock()
	if wg, ok := t.inflight[key]; ok {
		t.mu.Unlock()
		wg.Wait()
		return t.Get(ctx, f, m, ref, size)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	t.inflight[key] = wg
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.inflight, key)
		t.mu.Unlock()
		wg.Done()
	}()

	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	// Generation continues if the client goes away; the result is cached.
	genCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	data, err := t.render(genCtx, f, m, ref, size)
	if err != nil {
		log.Printf("thumbnail %s/%s: %v", f.Root, f.Path, err)
		if errors.Is(err, errRetry) {
			return "", errNoThumb // do not cache network failures
		}
		data = nil
	}
	if err := writeFile(name, data); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errNoThumb
	}
	return name, nil
}

func (t *Thumbs) render(ctx context.Context, f store.File, m media.Meta, ref, size string) ([]byte, error) {
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
		return io.ReadAll(io.LimitReader(body, 10<<20))
	}
	src, err := t.scanner.Abs(f)
	if err != nil {
		return nil, err
	}
	if entry, ok := strings.CutPrefix(ref, "zip:"); ok {
		z, err := zip.OpenReader(src)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		for _, file := range z.File {
			if file.Name == entry {
				r, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				data, err := io.ReadAll(io.LimitReader(r, 32<<20))
				if err != nil {
					return nil, err
				}
				return scale(bytes.NewReader(data), "")
			}
		}
		return nil, fmt.Errorf("%s not in archive", entry)
	}
	switch {
	case f.Kind == "image" && goImage[media.Ext(f.Name)]:
		file, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		if data, err := scale(file, media.Ext(f.Name)); err == nil {
			return data, nil
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
func (t *Thumbs) frame(ctx context.Context, src string, at float64) ([]byte, error) {
	args := []string{"-v", "error", "-nostdin"}
	if at >= 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 2, 64))
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", thumbSize, thumbSize),
		"-f", "image2", "-c:v", "mjpeg", "-q:v", "4", "pipe:1")
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if out.Len() == 0 && at > 0 {
		return t.frame(ctx, src, 0) // seek past the end of a short clip
	}
	return out.Bytes(), nil
}

func (t *Thumbs) pdf(ctx context.Context, src string) ([]byte, error) {
	if t.pdftoppm == "" {
		return nil, errNoThumb
	}
	out, err := exec.CommandContext(ctx, t.pdftoppm, "-f", "1", "-l", "1", "-singlefile", "-scale-to", strconv.Itoa(thumbSize), "-jpeg", src).Output()
	if err != nil {
		return nil, fmt.Errorf("pdftoppm: %w", err)
	}
	return out, nil
}

// scale decodes an image, applies EXIF orientation and fits it into the
// thumbnail box.
func scale(r io.ReadSeeker, ext string) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > 120_000_000 {
		return nil, fmt.Errorf("image too large: %dx%d", cfg.Width, cfg.Height)
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

func writeFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}
