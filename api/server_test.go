package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
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
