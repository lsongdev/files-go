package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestPosterDownloadsBoundedArtifact(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Movie.mkv", Path: "Movie.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(Candidate{ID: "1", Type: "movie", Title: "Movie", PosterPath: "/poster.jpg"})
	item, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: "Movie", ExternalID: "tmdb:1", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AssociateMediaFile(ctx, item.ID, entries[0].ID, "video"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-data"))
	}))
	defer server.Close()
	cacheDir := t.TempDir()
	poster := NewPoster(cat, cacheDir, server.Client())
	poster.baseURL = server.URL
	if err := poster.Process(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	artifact, err := cat.ArtifactForMedia(ctx, item.ID, "poster", "w500")
	if err != nil {
		t.Fatal(err)
	}
	parts := []byte(artifact.Key)
	dot := -1
	for index, value := range parts {
		if value == '.' {
			dot = index
		}
	}
	if dot < 0 {
		t.Fatalf("artifact key=%q", artifact.Key)
	}
	filename, err := ArtifactPath(cacheDir, "posters", artifact.Key[:dot], artifact.Key[dot+1:])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "jpeg-data" {
		t.Fatalf("poster=%q err=%v", data, err)
	}
	if _, err := ArtifactPath(cacheDir, "../escape", artifact.Key[:dot], "jpg"); err == nil {
		t.Fatal("unsafe artifact path accepted")
	}
}

func TestPosterDownloadsSeriesWithoutPlayableFile(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	metadata, _ := json.Marshal(Candidate{ID: "2", Type: "tv", Title: "Series", PosterPath: "/series.jpg"})
	item, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "series", Title: "Series", ExternalID: "tmdb:2", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("series-poster"))
	}))
	defer server.Close()
	poster := NewPoster(cat, t.TempDir(), server.Client())
	poster.baseURL = server.URL
	if err := poster.ProcessMedia(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ArtifactForMedia(ctx, item.ID, "poster", "w500"); err != nil {
		t.Fatalf("series poster was not stored: %v", err)
	}
}
