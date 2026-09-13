package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

type Poster struct {
	catalog           *catalog.Catalog
	cacheDir, baseURL string
	client            *http.Client
}

func NewPoster(catalog *catalog.Catalog, cacheDir string, client *http.Client) *Poster {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Poster{catalog: catalog, cacheDir: cacheDir, baseURL: "https://image.tmdb.org/t/p/w500", client: client}
}
func (p *Poster) Name() string { return "poster" }
func (p *Poster) Match(entry model.Entry) bool {
	if strings.HasSuffix(strings.ToLower(entry.Name), ".d.ts") {
		return false
	}
	switch strings.ToLower(entry.Extension) {
	case "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "wmv", "rmvb":
		return !strings.HasPrefix(entry.Name, "._")
	default:
		return false
	}
}

func (p *Poster) Process(ctx context.Context, entry model.Entry) error {
	item, err := p.catalog.MediaItemForEntry(ctx, entry.ID, "video")
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.ProcessMedia(ctx, *item)
}

// ProcessMedia downloads artwork for a media identity directly. Series and
// other parent items are not necessarily the playable item of any one file.
func (p *Poster) ProcessMedia(ctx context.Context, item model.MediaItem) error {
	var metadata Candidate
	if err := json.Unmarshal(item.Metadata, &metadata); err != nil || metadata.PosterPath == "" {
		return nil
	}
	posterPath := strings.TrimPrefix(metadata.PosterPath, "/")
	if posterPath == "" || path.Base(posterPath) != posterPath {
		return errors.New("invalid poster path")
	}
	key := posterKey(item.ID, posterPath)
	if _, err := p.catalog.ArtifactForMedia(ctx, item.ID, "poster", "w500"); err == nil {
		return nil
	} else if !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/"+posterPath, nil)
	if err != nil {
		return err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return fmt.Errorf("poster returned HTTP %d", res.StatusCode)
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0]))
	extension := ""
	switch mimeType {
	case "image/jpeg":
		extension = "jpg"
	case "image/png":
		extension = "png"
	default:
		return fmt.Errorf("unsupported poster content type %q", mimeType)
	}
	filename, err := ArtifactPath(p.cacheDir, "posters", key, extension)
	if err != nil {
		return err
	}
	size, err := writeLimitedFile(filename, res.Body, 10<<20)
	if err != nil {
		return err
	}
	_, err = p.catalog.UpsertArtifact(ctx, model.Artifact{MediaID: item.ID, Type: "poster", Variant: "w500", Key: key + "." + extension, MIME: mimeType, Size: size})
	return err
}

func posterKey(mediaID, posterPath string) string {
	sum := sha256.Sum256([]byte(mediaID + ":w500:" + posterPath))
	return hex.EncodeToString(sum[:])
}

func ArtifactPath(cacheDir, directory, key, extension string) (string, error) {
	key = strings.TrimSuffix(key, "."+extension)
	if len(key) != 64 {
		return "", errors.New("invalid artifact key")
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", errors.New("invalid artifact key")
	}
	if directory != "posters" || (extension != "jpg" && extension != "png") {
		return "", errors.New("invalid artifact path")
	}
	return filepath.Join(cacheDir, directory, key[:2], key[2:4], key+"."+extension), nil
}

func writeLimitedFile(filename string, source io.Reader, limit int64) (size int64, err error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".artifact-*.tmp")
	if err != nil {
		return 0, err
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	size, err = io.Copy(file, io.LimitReader(source, limit+1))
	if err != nil {
		return 0, err
	}
	if size > limit {
		return 0, errors.New("artifact exceeds size limit")
	}
	if err = file.Sync(); err != nil {
		return 0, err
	}
	if err = file.Close(); err != nil {
		return 0, err
	}
	return size, os.Rename(temporary, filename)
}
