package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/jobs"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

type apiMetadataProvider struct{}

func TestScanEntryQueuesOnlyConfiguredFile(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "Film.mkv", Path: "Movies/Film.mkv", Type: model.EntryFile, Extension: "mkv"},
		{StorageID: "disk", Name: "Other.mkv", Path: "Projects/Other.mkv", Type: model.EntryFile, Extension: "mkv"},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	idx := indexer.New(cat, storage.NewRegistry())
	if err := idx.SetScanScope("disk", []string{"Movies"}); err != nil {
		t.Fatal(err)
	}
	queue := jobs.New(db, time.Minute)
	idx.SetEntrySink(processor.New(cat, queue))
	server := New(ctx, cat, storage.NewRegistry(), idx, log.Default(), t.TempDir())
	for _, check := range []struct {
		entry model.Entry
		code  int
	}{
		{entries[0], http.StatusAccepted}, {entries[1], http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/entries/"+check.entry.ID+"/scan", nil))
		if response.Code != check.code {
			t.Fatalf("scan %s = %d %s", check.entry.Path, response.Code, response.Body.String())
		}
	}
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='pending'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued jobs = %d, %v", queued, err)
	}
}

func TestMediaImageEndpointsResolvePublicFields(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Film", Path: "Film", Type: model.EntryDirectory}, {StorageID: "disk", Name: "folder.jpg", Path: "folder.jpg", Type: model.EntryFile, Extension: "jpg"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	server := New(ctx, cat, storage.NewRegistry(), nil, log.Default(), cacheDir)
	if _, err := cat.SetMediaCandidate(ctx, entries[0].ID, "local_artwork", catalog.MediaCandidate{Icon: "file:" + entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entries[0].ID+"/icon", nil))
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/api/v1/entries/"+entries[1].ID+"/thumbnail?size=medium" {
		t.Fatalf("icon response = %d, location %q", response.Code, response.Header().Get("Location"))
	}
	key := strings.Repeat("a", 64)
	filename, err := mediaengine.ArtifactPath(cacheDir, "posters", key, "jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("cached poster"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetMediaCandidate(ctx, entries[0].ID, "tmdb", catalog.MediaCandidate{Backdrop: "cache:posters/" + key + ".jpg"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entries[0].ID+"/backdrop", nil))
	if response.Code != http.StatusOK || response.Body.String() != "cached poster" || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("backdrop response = %d %q %q", response.Code, response.Body.String(), response.Header().Get("Content-Type"))
	}
}

func TestEntryMediaProjectionMatchesListAndDetail(t *testing.T) {
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
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &root.ID, Name: "Modern.Times.1936.mkv", Path: "Modern.Times.1936.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	year := 1936
	if _, err := cat.SetMediaCandidate(ctx, entry.ID, "tmdb", catalog.MediaCandidate{Kind: "movie", Title: "摩登时代", Year: &year, Icon: "cache:posters/poster.jpg", Backdrop: "cache:posters/backdrop.jpg"}); err != nil {
		t.Fatal(err)
	}
	server := New(ctx, cat, storage.NewRegistry(), nil, log.Default(), t.TempDir())
	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+root.ID+"/children", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	var listing struct {
		Items []entryResponse `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listing); err != nil || len(listing.Items) != 1 {
		t.Fatalf("list = %#v, %v", listing, err)
	}
	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entry.ID, nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	var single entryResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &single); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listing.Items[0].Media, single.Media) || !reflect.DeepEqual(listing.Items[0].Links, single.Links) {
		t.Fatalf("list/detail media differ: list=%#v detail=%#v", listing.Items[0], single)
	}
	if single.Media == nil || single.Media.Title != "摩登时代" || single.Links["thumbnail"] != single.Media.Icon {
		t.Fatalf("resolved media projection = %#v", single)
	}
}

func (apiMetadataProvider) Search(_ context.Context, query mediaengine.Query) ([]mediaengine.Candidate, error) {
	year := 2014
	return []mediaengine.Candidate{{ID: "157336", Type: query.Type, Title: "Interstellar", Year: &year}}, nil
}

func (apiMetadataProvider) Fetch(_ context.Context, itemType, id, _ string) (mediaengine.Candidate, error) {
	year := 2014
	return mediaengine.Candidate{ID: id, Type: itemType, Title: "Interstellar", Year: &year, PosterPath: "/poster.jpg"}, nil
}

func TestManualMediaCandidateAPI(t *testing.T) {
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
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}}); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Interstellar.2014.mkv", Path: "Movies/Interstellar.2014.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	server := New(ctx, cat, registry, indexer.New(cat, registry), log.Default(), t.TempDir())
	server.SetMediaProvider(apiMetadataProvider{}, "en-US")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entries[0].ID+"/media-candidates?q=Interstellar", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"157336"`) {
		t.Fatalf("candidates = %d %s", response.Code, response.Body.String())
	}
	payload := strings.NewReader(`{"candidateId":"157336","candidateType":"movie"}`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/entries/"+entries[0].ID+"/media", payload))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"matchLocked":true`) {
		t.Fatalf("manual match = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=Interstellar", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"media":{"kind":"movie"`) || !strings.Contains(response.Body.String(), `"title":"Interstellar"`) {
		t.Fatalf("entry media summary = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+entries[0].ID+"/media", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unmatch = %d %s", response.Code, response.Body.String())
	}
	if resolved, err := cat.MediaForEntry(ctx, entries[0].ID); err != nil || !resolved.MatchLocked || resolved.Title == "Interstellar" {
		t.Fatalf("suppressed media = %#v, %v", resolved, err)
	}
}

func TestSidecarFileDetailDoesNotInheritMovieIdentity(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "Movie.mkv", Path: "Movie.mkv", Type: model.EntryFile},
		{StorageID: "disk", Name: "folder.jpg", Path: "folder.jpg", Type: model.EntryFile},
		{StorageID: "disk", Name: "movie.nfo", Path: "movie.nfo", Type: model.EntryFile},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetMediaCandidate(ctx, entries[0].ID, "filename", catalog.MediaCandidate{Kind: "movie", Title: "Movie"}); err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	server := New(ctx, cat, registry, indexer.New(cat, registry), log.Default(), t.TempDir())
	for _, entry := range entries[1:] {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entry.ID+"/media?optional=1", nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s inherited movie detail: %d %s", entry.Name, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entries[0].ID+"/media?optional=1", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"title":"Movie"`) {
		t.Fatalf("video movie detail = %d %s", response.Code, response.Body.String())
	}
}

func TestSystemStatusReportsProcessingQueue(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := jobs.New(db, time.Minute)
	if _, _, err := queue.Enqueue(ctx, processor.JobProcessEntry, map[string]string{"entryId": "one"}, jobs.EnqueueOptions{Key: "one"}); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	registry := storage.NewRegistry()
	server := New(ctx, cat, registry, indexer.New(cat, registry), log.Default(), t.TempDir())
	server.SetJobQueue(queue)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"pending":1`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/failures", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("failure groups = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestThumbnailPendingDoesNotProduceNoisyNotFound(t *testing.T) {
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
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "photo.jpg", Path: "photo.jpg", Type: model.EntryFile, Extension: "jpg"}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	server := New(ctx, cat, registry, indexer.New(cat, registry), log.Default(), t.TempDir())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+entries[0].ID+"/thumbnail?size=medium", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pending thumbnail = %d, headers = %#v", response.Code, response.Header())
	}
}

func TestDecodeText(t *testing.T) {
	utf16LE := []byte{0xff, 0xfe, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(utf16LE[2:], 'A')
	binary.LittleEndian.PutUint16(utf16LE[4:], '中')
	if got, ok := decodeText(utf16LE); !ok || got != "A中" {
		t.Fatalf("UTF-16 decode = %q, %v", got, ok)
	}
	if _, ok := decodeText([]byte{'a', 0, 'b'}); ok {
		t.Fatal("binary data was accepted as text")
	}
}

func TestEntryAPIHidesPathsBrowsesOfflineAndServesRange(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "private-mount")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("你好，files-go\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{'a', 0, 'b'}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Season"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Season", "episode.txt"), []byte("episode"), 0644); err != nil {
		t.Fatal(err)
	}
	photoFile, err := os.Create(filepath.Join(root, "photo.png"))
	if err != nil {
		t.Fatal(err)
	}
	photoImage := image.NewRGBA(image.Rect(0, 0, 4, 3))
	photoImage.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := png.Encode(photoFile, photoImage); err != nil {
		_ = photoFile.Close()
		t.Fatal(err)
	}
	if err := photoFile.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: ""}}}); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	idx := indexer.New(cat, registry)
	if err := idx.Scan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	rootEntry, err := cat.EntryByPath(ctx, "disk", "")
	if err != nil {
		t.Fatal(err)
	}
	movie, err := cat.EntryByPath(ctx, "disk", "movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := cat.EntryByPath(ctx, "disk", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	binaryFile, err := cat.EntryByPath(ctx, "disk", "binary.dat")
	if err != nil {
		t.Fatal(err)
	}
	season, err := cat.EntryByPath(ctx, "disk", "Season")
	if err != nil {
		t.Fatal(err)
	}
	episode, err := cat.EntryByPath(ctx, "disk", "Season/episode.txt")
	if err != nil {
		t.Fatal(err)
	}
	photo, err := cat.EntryByPath(ctx, "disk", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	if err := processor.NewImageMetadata(cat, registry).Process(ctx, *photo); err != nil {
		t.Fatal(err)
	}
	if err := processor.NewThumbnail(cat, registry, cacheDir).Process(ctx, *photo); err != nil {
		t.Fatal(err)
	}
	var apiLogs bytes.Buffer
	handler := New(ctx, cat, registry, idx, log.New(&apiLogs, "", 0), cacheDir).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+rootEntry.ID+"/children?limit=1", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("children status = %d, body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), root) || strings.Contains(res.Body.String(), "private-mount") || strings.Contains(res.Body.String(), `"path"`) {
		t.Fatalf("API leaked storage path: %s", res.Body.String())
	}
	var listing struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &listing); err != nil || len(listing.Items) != 1 {
		t.Fatalf("invalid listing response: %v %s", err, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+movie.ID+"/content", nil)
	req.Header.Set("Range", "bytes=2-5")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusPartialContent || res.Body.String() != "2345" {
		t.Fatalf("range response = %d %q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
	if res.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe content headers: %#v", res.Header())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+notes.ID+"/text", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "你好，files-go\n" || res.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("text preview = %d %q %q", res.Code, res.Body.String(), res.Header().Get("Content-Type"))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+binaryFile.ID+"/text", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType || !strings.Contains(res.Body.String(), "binary_file") {
		t.Fatalf("binary preview = %d %s", res.Code, res.Body.String())
	}

	for _, target := range []string{
		"/api/v1/entries/" + notes.ID + "/media?optional=1",
	} {
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, target, nil))
		if res.Code != http.StatusNoContent {
			t.Fatalf("optional media lookup %s = %d %s", target, res.Code, res.Body.String())
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+photo.ID+"/media", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"kind":"photo"`) || !strings.Contains(res.Body.String(), `"width":4`) {
		t.Fatalf("media metadata = %d %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+photo.ID+"/thumbnail?size=small", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "image/jpeg" || res.Body.Len() == 0 || !strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("thumbnail = %d %q %d %#v", res.Code, res.Header().Get("Content-Type"), res.Body.Len(), res.Header())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/search?q=mov&library=movies&type=file&extension=.MP4", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "movie.mp4") {
		t.Fatalf("search response = %d %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), root) || strings.Contains(res.Body.String(), `"path"`) {
		t.Fatalf("search API leaked storage path: %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, `/api/v1/search?q=%22%20OR%20%2A`, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("quoted search response = %d %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/entries/"+rootEntry.ID+"/directories", strings.NewReader(`{"name":"Drafts"}`))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create directory = %d %s logs=%s", res.Code, res.Body.String(), apiLogs.String())
	}
	var drafts entryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &drafts); err != nil || drafts.Name != "Drafts" {
		t.Fatalf("created directory response = %#v, %v", drafts, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Drafts")); err != nil {
		t.Fatalf("created directory missing: %v", err)
	}
	copyPayload, err := json.Marshal(map[string]string{"parentId": drafts.ID, "name": "Season Copy"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/entries/"+season.ID+"/copies", bytes.NewReader(copyPayload))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("copy directory = %d %s logs=%s", res.Code, res.Body.String(), apiLogs.String())
	}
	var copiedSeason entryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &copiedSeason); err != nil {
		t.Fatal(err)
	}
	copiedEpisode, err := cat.EntryByPath(ctx, "disk", "Drafts/Season Copy/episode.txt")
	if err != nil {
		t.Fatalf("copied descendant missing from catalog: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "Drafts", "Season Copy", "episode.txt")); err != nil || string(data) != "episode" {
		t.Fatalf("copied descendant content = %q, %v", data, err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/entries/"+season.ID+"/copies", bytes.NewReader(copyPayload))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "entry_exists") {
		t.Fatalf("duplicate copy = %d %s", res.Code, res.Body.String())
	}
	for _, copiedID := range []string{copiedEpisode.ID, copiedSeason.ID} {
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+copiedID, nil)
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("delete copied entry %s = %d %s", copiedID, res.Code, res.Body.String())
		}
	}
	movePayload, err := json.Marshal(map[string]string{"parentId": drafts.ID})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/entries/"+binaryFile.ID, bytes.NewReader(movePayload))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("move file = %d %s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Drafts", "binary.dat")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := cat.EntryByPath(ctx, "disk", "Drafts/binary.dat"); err != nil {
		t.Fatalf("moved catalog entry missing: %v", err)
	}
	movePayload, err = json.Marshal(map[string]string{"parentId": rootEntry.ID})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/entries/"+binaryFile.ID, bytes.NewReader(movePayload))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("move file back = %d %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/entries/"+season.ID, strings.NewReader(`{"name":"Series"}`))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("rename directory = %d %s", res.Code, res.Body.String())
	}
	if _, err := cat.EntryByPath(ctx, "disk", "Series/episode.txt"); err != nil {
		t.Fatalf("descendant catalog path was not updated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Series", "episode.txt")); err != nil {
		t.Fatalf("renamed directory missing: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+season.ID, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "directory_not_empty") {
		t.Fatalf("non-empty delete = %d %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+episode.ID, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete file = %d %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+season.ID, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete empty directory = %d %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/entries/"+drafts.ID, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete created directory = %d %s", res.Code, res.Body.String())
	}
	if _, err := cat.Entry(ctx, drafts.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("deleted catalog entry error = %v", err)
	}

	var upload bytes.Buffer
	multipartWriter := multipart.NewWriter(&upload)
	part, err := multipartWriter.CreateFormFile("file", "uploaded notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("streamed upload")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/entries/"+rootEntry.ID+"/files", &upload)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("upload file = %d %s", res.Code, res.Body.String())
	}
	var uploaded entryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &uploaded); err != nil || uploaded.Name != "uploaded notes.txt" || uploaded.Size != 15 {
		t.Fatalf("uploaded response = %#v, %v", uploaded, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "uploaded notes.txt")); err != nil || string(data) != "streamed upload" {
		t.Fatalf("uploaded content = %q, %v", data, err)
	}
	if found, err := cat.EntryByPath(ctx, "disk", "uploaded notes.txt"); err != nil || found.ID != uploaded.ID {
		t.Fatalf("uploaded catalog entry = %#v, %v", found, err)
	}

	// Catalog browsing remains functional without touching the now-missing disk.
	if err := os.Rename(root, root+"-detached"); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/entries/"+rootEntry.ID+"/children", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "movie.mp4") {
		t.Fatalf("offline catalog listing = %d %s", res.Code, res.Body.String())
	}
}
