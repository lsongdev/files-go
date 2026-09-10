package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type Server struct {
	ctx      context.Context
	catalog  *catalog.Catalog
	storages *storage.Registry
	indexer  *indexer.Indexer
	logger   *log.Logger
	mux      *http.ServeMux
}

func New(ctx context.Context, catalog *catalog.Catalog, storages *storage.Registry, indexer *indexer.Indexer, logger *log.Logger) *Server {
	server := &Server{ctx: ctx, catalog: catalog, storages: storages, indexer: indexer, logger: logger, mux: http.NewServeMux()}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/system", s.system)
	s.mux.HandleFunc("GET /api/v1/storages", s.listStorages)
	s.mux.HandleFunc("GET /api/v1/storages/{id}", s.getStorage)
	s.mux.HandleFunc("POST /api/v1/storages/{id}/scan", s.scanStorage)
	s.mux.HandleFunc("GET /api/v1/libraries", s.listLibraries)
	s.mux.HandleFunc("GET /api/v1/search", s.search)
	s.mux.HandleFunc("GET /api/v1/entries/{id}", s.getEntry)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/children", s.listChildren)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/content", s.content)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "q must be between 1 and 200 characters")
		return
	}
	limit := 100
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
			return
		}
	}
	entryType := model.EntryType(r.URL.Query().Get("type"))
	if entryType != "" && entryType != model.EntryFile && entryType != model.EntryDirectory && entryType != model.EntrySymlink {
		writeError(w, http.StatusBadRequest, "invalid_request", "type must be file, directory, or symlink")
		return
	}
	items, err := s.catalog.Search(r.Context(), catalog.SearchOptions{
		Query: query, LibraryID: r.URL.Query().Get("library"), Type: entryType,
		Extension: r.URL.Query().Get("extension"), Limit: limit,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	result := make([]entryResponse, len(items))
	for index := range items {
		result[index] = responseFor(items[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": "0.1.0", "features": map[string]bool{"media": false, "transcode": false}})
}

func (s *Server) listStorages(w http.ResponseWriter, r *http.Request) {
	items, err := s.catalog.Storages(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getStorage(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Storage(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "storage_not_found", "storage not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) scanStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.storages.Get(id); !ok {
		writeError(w, http.StatusNotFound, "storage_not_found", "storage not found")
		return
	}
	go func() {
		if err := s.indexer.Scan(s.ctx, id); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, indexer.ErrScanInProgress) {
			s.logger.Printf("scan storage %s: %v", id, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scanning"})
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	items, err := s.catalog.Libraries(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type entryResponse struct {
	ID         string            `json:"id"`
	ParentID   *string           `json:"parentId,omitempty"`
	Name       string            `json:"name"`
	Type       model.EntryType   `json:"type"`
	Size       int64             `json:"size"`
	MIME       string            `json:"mime,omitempty"`
	Extension  string            `json:"extension,omitempty"`
	Available  bool              `json:"available"`
	ModifiedAt any               `json:"modifiedAt,omitempty"`
	CreatedAt  any               `json:"createdAt"`
	UpdatedAt  any               `json:"updatedAt"`
	Links      map[string]string `json:"links"`
}

func responseFor(entry model.Entry) entryResponse {
	var modified any
	if !entry.ModifiedAt.IsZero() {
		modified = entry.ModifiedAt
	}
	links := map[string]string{}
	if entry.Type == model.EntryDirectory {
		links["children"] = "/api/v1/entries/" + entry.ID + "/children"
	}
	if entry.Type == model.EntryFile {
		links["content"] = "/api/v1/entries/" + entry.ID + "/content"
	}
	return entryResponse{ID: entry.ID, ParentID: entry.ParentID, Name: entry.Name, Type: entry.Type,
		Size: entry.Size, MIME: entry.MIME, Extension: entry.Extension, Available: entry.Available,
		ModifiedAt: modified, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt, Links: links}
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, responseFor(*entry))
}

func (s *Server) listChildren(w http.ResponseWriter, r *http.Request) {
	parent, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if parent.Type != model.EntryDirectory {
		writeError(w, http.StatusBadRequest, "invalid_request", "entry is not a directory")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
			return
		}
	}
	var after *catalog.ListCursor
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = decodeCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid cursor")
			return
		}
	}
	items, err := s.catalog.Children(r.Context(), parent.ID, catalog.ListOptions{Limit: limit, After: after})
	if err != nil {
		s.internalError(w, err)
		return
	}
	var next any
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last)
		items = items[:limit]
	}
	result := make([]entryResponse, len(items))
	for index := range items {
		result[index] = responseFor(items[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "cursor": next})
}

func encodeCursor(entry model.Entry) string {
	rank := 1
	if entry.Type == model.EntryDirectory {
		rank = 0
	}
	data, _ := json.Marshal(catalog.ListCursor{DirectoryRank: rank, Name: strings.ToLower(entry.Name), ID: entry.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (*catalog.ListCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var cursor catalog.ListCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	if cursor.DirectoryRank < 0 || cursor.DirectoryRank > 1 || cursor.ID == "" {
		return nil, errors.New("invalid cursor")
	}
	return &cursor, nil
}

func (s *Server) content(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if entry.Type != model.EntryFile {
		writeError(w, http.StatusBadRequest, "invalid_request", "entry is not a file")
		return
	}
	state, err := s.catalog.Storage(r.Context(), entry.StorageID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if state.State == "offline" {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	if !entry.Available {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry is not available")
		return
	}
	backend, ok := s.storages.Get(entry.StorageID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	file, err := backend.Open(r.Context(), entry.Path)
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrOffline) {
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		s.internalError(w, fmt.Errorf("open entry %s: %w", entry.ID, err))
		return
	}
	defer file.Close()
	contentType := entry.MIME
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := mime.FormatMediaType("inline", map[string]string{"filename": entry.Name})
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	etag := fmt.Sprintf("\"%d-%d\"", entry.Size, entry.ModifiedAt.UnixMilli())
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match == etag || match == "*" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, entry.Name, entry.ModifiedAt, file)
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Printf("api: %v", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
