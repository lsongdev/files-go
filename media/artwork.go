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
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

// Artwork materializes a file or directory's TMDB image references into the
// same candidate that supplies its title. There is no separate media identity.
type Artwork struct {
	catalog  *catalog.Catalog
	cacheDir string
	client   *http.Client
	baseURL  string
}

func NewArtwork(cat *catalog.Catalog, cacheDir string, client *http.Client) *Artwork {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Artwork{catalog: cat, cacheDir: cacheDir, client: client, baseURL: "https://image.tmdb.org/t/p"}
}

func (p *Artwork) Name() string { return "tmdb_artwork" }
func (p *Artwork) Match(entry model.Entry) bool {
	return entry.Type == model.EntryFile && movieVideoExtension(entry.Extension)
}

func (p *Artwork) Process(ctx context.Context, entry model.Entry) error {
	return p.ProcessEntry(ctx, entry.ID)
}

func (p *Artwork) ProcessEntry(ctx context.Context, entryID string) error {
	item, err := p.catalog.MediaForEntry(ctx, entryID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var sources map[string]catalog.MediaCandidate
	if err := json.Unmarshal(item.Sources, &sources); err != nil {
		return err
	}
	for _, source := range []string{"manual", "tmdb"} {
		candidate, ok := sources[source]
		if !ok || len(candidate.Data) == 0 {
			continue
		}
		var provider Candidate
		if err := json.Unmarshal(candidate.Data, &provider); err != nil {
			return err
		}
		changed := false
		if candidate.Icon == "" && provider.PosterPath != "" {
			ref, err := p.cache(ctx, entryID, "w500", provider.PosterPath)
			if err != nil {
				return err
			}
			candidate.Icon, changed = ref, true
		}
		if candidate.Backdrop == "" && provider.BackdropPath != "" {
			ref, err := p.cache(ctx, entryID, "w1280", provider.BackdropPath)
			if err != nil {
				return err
			}
			candidate.Backdrop, changed = ref, true
		}
		if changed {
			if _, err := p.catalog.SetMediaCandidate(ctx, entryID, source, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Artwork) cache(ctx context.Context, entryID, size, artworkPath string) (string, error) {
	name := strings.TrimPrefix(artworkPath, "/")
	if name == "" || path.Base(name) != name || (size != "w500" && size != "w1280") {
		return "", errors.New("invalid TMDB artwork path")
	}
	sum := sha256.Sum256([]byte(entryID + ":" + size + ":" + name))
	key := hex.EncodeToString(sum[:])
	for _, ext := range []string{"jpg", "png"} {
		filename, _ := ArtifactPath(p.cacheDir, "posters", key, ext)
		if info, err := os.Stat(filename); err == nil && info.Size() > 0 {
			return "cache:posters/" + key + "." + ext, nil
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/"+size+"/"+name, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return "", fmt.Errorf("TMDB artwork returned HTTP %d", response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	ext := ""
	switch contentType {
	case "image/jpeg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	default:
		return "", fmt.Errorf("unsupported artwork content type %q", contentType)
	}
	filename, err := ArtifactPath(p.cacheDir, "posters", key, ext)
	if err != nil {
		return "", err
	}
	if _, err := writeLimitedFile(filename, response.Body, 10<<20); err != nil {
		return "", err
	}
	return "cache:posters/" + key + "." + ext, nil
}
