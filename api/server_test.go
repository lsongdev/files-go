package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

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
	handler := New(ctx, cat, registry, idx, log.New(io.Discard, "", 0)).Handler()

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
		t.Fatalf("create directory = %d %s", res.Code, res.Body.String())
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
		t.Fatalf("copy directory = %d %s", res.Code, res.Body.String())
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
