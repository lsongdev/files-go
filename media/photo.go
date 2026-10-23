package media

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"

	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/lsongdev/files-go/store"
)

// Photo reads dimensions and EXIF (capture time, camera, lens, GPS).
type Photo struct{ Path Resolver }

func (Photo) Match(f *store.File) bool {
	switch Ext(f.Name) {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff":
		return f.Kind == "image"
	}
	return false
}

func (p Photo) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	name, err := p.Path(*f)
	if err != nil {
		return err
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	m.Kind = "photo"
	cfg, format, err := image.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	m.Width, m.Height = cfg.Width, cfg.Height
	m.Set("Format", strings.ToUpper(format))
	if format != "jpeg" && format != "tiff" {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	x, err := exif.Decode(file)
	if err != nil {
		return nil // most images simply have no EXIF
	}
	if t, err := x.DateTime(); err == nil {
		m.Year = t.Year()
		m.Set("Taken", t.Format("2006-01-02 15:04:05"))
	}
	camera := strings.TrimSpace(exifString(x, exif.Make) + " " + exifString(x, exif.Model))
	m.Set("Camera", camera)
	m.Set("Lens", exifString(x, exif.LensModel))
	m.Set("Exposure", exifValue(x, exif.ExposureTime))
	m.Set("Aperture", exifValue(x, exif.FNumber))
	m.Set("ISO", exifValue(x, exif.ISOSpeedRatings))
	m.Set("Focal length", exifValue(x, exif.FocalLength))
	if lat, lng, err := x.LatLong(); err == nil {
		m.Set("Location", fmt.Sprintf("%.5f, %.5f", lat, lng))
	}
	if o := exifValue(x, exif.Orientation); o == "5" || o == "6" || o == "7" || o == "8" {
		m.Width, m.Height = m.Height, m.Width // rotated by 90°
	}
	return nil
}

func exifString(x *exif.Exif, field exif.FieldName) string {
	tag, err := x.Get(field)
	if err != nil {
		return ""
	}
	s, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(s, "\x00"))
}

// exifValue renders numeric tags; rationals become e.g. "1/125" or "2.8".
func exifValue(x *exif.Exif, field exif.FieldName) string {
	tag, err := x.Get(field)
	if err != nil {
		return ""
	}
	if num, den, err := tag.Rat2(0); err == nil && den != 0 {
		if field == exif.ExposureTime && num < den {
			return fmt.Sprintf("1/%d", den/max(num, 1))
		}
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", float64(num)/float64(den)), "0"), ".")
	}
	if v, err := tag.Int(0); err == nil {
		return fmt.Sprint(v)
	}
	return strings.Trim(tag.String(), `"`)
}
