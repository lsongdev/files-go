package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/jobs"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/playback"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

type Server struct {
	ctx      context.Context
	catalog  *catalog.Catalog
	storages *storage.Registry
	indexer  *indexer.Indexer
	logger   *log.Logger
	mux      *http.ServeMux
	cacheDir string
	playback *playback.Manager
	jobQueue *jobs.Queue
	provider mediaengine.MetadataProvider
	language string
	artwork  *mediaengine.Artwork
}

func New(ctx context.Context, catalog *catalog.Catalog, storages *storage.Registry, indexer *indexer.Indexer, logger *log.Logger, cacheDir string, managers ...*playback.Manager) *Server {
	server := &Server{ctx: ctx, catalog: catalog, storages: storages, indexer: indexer, logger: logger, mux: http.NewServeMux(), cacheDir: cacheDir}
	if len(managers) > 0 {
		server.playback = managers[0]
	}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) SetJobQueue(queue *jobs.Queue) { s.jobQueue = queue }
func (s *Server) SetMediaProvider(provider mediaengine.MetadataProvider, language string) {
	s.provider, s.language = provider, language
	s.artwork = mediaengine.NewArtwork(s.catalog, s.cacheDir, nil)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/system", s.system)
	s.mux.HandleFunc("GET /api/v1/system/status", s.systemStatus)
	s.mux.HandleFunc("GET /api/v1/system/failures", s.systemFailures)
	s.mux.HandleFunc("GET /api/v1/storages", s.listStorages)
	s.mux.HandleFunc("GET /api/v1/storages/{id}", s.getStorage)
	s.mux.HandleFunc("POST /api/v1/storages/{id}/scan", s.scanStorage)
	s.mux.HandleFunc("GET /api/v1/libraries", s.listLibraries)
	s.mux.HandleFunc("GET /api/v1/search", s.search)
	s.mux.HandleFunc("GET /api/v1/entries/{id}", s.getEntry)
	s.mux.HandleFunc("POST /api/v1/entries/{id}/scan", s.scanEntrySubtree)
	s.mux.HandleFunc("PATCH /api/v1/entries/{id}", s.updateEntry)
	s.mux.HandleFunc("DELETE /api/v1/entries/{id}", s.deleteEntry)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/children", s.listChildren)
	s.mux.HandleFunc("POST /api/v1/entries/{id}/directories", s.createDirectory)
	s.mux.HandleFunc("POST /api/v1/entries/{id}/files", s.uploadFile)
	s.mux.HandleFunc("POST /api/v1/entries/{id}/copies", s.copyEntry)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/content", s.content)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/text", s.text)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/media", s.getEntryMediaV2)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/thumbnail", s.thumbnail)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/icon", s.mediaIcon)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/backdrop", s.mediaBackdrop)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/media-candidates", s.searchMediaCandidatesV2)
	s.mux.HandleFunc("PUT /api/v1/entries/{id}/media", s.setEntryMediaV2)
	s.mux.HandleFunc("DELETE /api/v1/entries/{id}/media", s.unmatchEntryMediaV2)
	s.mux.HandleFunc("POST /api/v1/entries/{id}/media/rematch", s.rematchEntryMediaV2)
	s.mux.HandleFunc("POST /api/v1/playback/{id}", s.startPlayback)
	s.mux.HandleFunc("GET /api/v1/playback/sessions/{session}/{file}", s.playbackFile)
	s.mux.HandleFunc("DELETE /api/v1/playback/sessions/{session}", s.stopPlayback)
	s.mux.HandleFunc("GET /api/v1/entries/{id}/playback-state", s.getPlaybackState)
	s.mux.HandleFunc("PUT /api/v1/entries/{id}/playback-state", s.setPlaybackState)
	s.mux.HandleFunc("GET /api/v1/playback/continue", s.continueWatching)
}

func (s *Server) startPlayback(w http.ResponseWriter, r *http.Request) {
	if s.playback == nil {
		writeError(w, http.StatusServiceUnavailable, "playback_unavailable", "playback service is unavailable")
		return
	}
	var capabilities playback.Capabilities
	if err := decodeJSONBody(w, r, &capabilities); err != nil {
		return
	}
	result, err := s.playback.Start(r.Context(), r.PathValue("id"), capabilities)
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "media_not_found", "entry or media metadata not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) playbackFile(w http.ResponseWriter, r *http.Request) {
	if s.playback == nil {
		writeError(w, http.StatusNotFound, "session_not_found", "playback session not found")
		return
	}
	filename, err := s.playback.File(r.PathValue("session"), r.PathValue("file"))
	if err != nil {
		writeError(w, http.StatusNotFound, "segment_not_ready", "playback output is not ready")
		return
	}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusNotFound, "segment_not_ready", "playback output is not ready")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if strings.HasSuffix(filename, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(filename), info.ModTime(), file)
}

func (s *Server) stopPlayback(w http.ResponseWriter, r *http.Request) {
	if s.playback == nil || !s.playback.Stop(r.PathValue("session")) {
		writeError(w, http.StatusNotFound, "session_not_found", "playback session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type playbackStateInput struct {
	PositionMS int64 `json:"positionMs"`
	Played     bool  `json:"played"`
}

func playbackUser(r *http.Request) (string, error) {
	user := strings.TrimSpace(r.Header.Get("X-Files-Go-User"))
	if user == "" {
		return "local", nil
	}
	if len(user) > 128 {
		return "", errors.New("user ID is too long")
	}
	for _, char := range user {
		if !(char == '-' || char == '_' || char == '.' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
			return "", errors.New("user ID contains unsupported characters")
		}
	}
	return user, nil
}

func (s *Server) getPlaybackState(w http.ResponseWriter, r *http.Request) {
	user, err := playbackUser(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state, err := s.catalog.PlaybackState(r.Context(), user, r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "playback_state_not_found", "playback state not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) setPlaybackState(w http.ResponseWriter, r *http.Request) {
	user, err := playbackUser(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, err := s.catalog.Entry(r.Context(), r.PathValue("id")); errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var input playbackStateInput
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	state, err := s.catalog.UpsertPlaybackState(r.Context(), model.PlaybackState{UserID: user, EntryID: r.PathValue("id"), PositionMS: input.PositionMS, Played: input.Played})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) continueWatching(w http.ResponseWriter, r *http.Request) {
	user, err := playbackUser(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, err := s.catalog.ContinueWatching(r.Context(), user, 20)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	variant := r.URL.Query().Get("size")
	if variant == "" {
		variant = "medium"
	}
	if variant != "small" && variant != "medium" && variant != "large" {
		writeError(w, http.StatusBadRequest, "invalid_request", "size must be small, medium, or large")
		return
	}
	artifact, err := s.catalog.ArtifactForEntry(r.Context(), r.PathValue("id"), "thumbnail", variant)
	if errors.Is(err, catalog.ErrNotFound) {
		if entry, entryErr := s.catalog.Entry(r.Context(), r.PathValue("id")); entryErr == nil {
			s.reprocess(*entry)
		}
		writeThumbnailPending(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	filename, err := processor.ThumbnailPath(s.cacheDir, artifact.Key)
	if err != nil {
		s.internalError(w, err)
		return
	}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		if entry, entryErr := s.catalog.Entry(r.Context(), r.PathValue("id")); entryErr == nil {
			s.reprocess(*entry)
		}
		writeThumbnailPending(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+artifact.Key+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, artifact.ID+".jpg", info.ModTime(), file)
	go func() { _ = s.catalog.TouchArtifact(s.ctx, artifact.ID) }()
}

func (s *Server) mediaIcon(w http.ResponseWriter, r *http.Request) {
	s.serveMediaImage(w, r, "icon")
}

func (s *Server) mediaBackdrop(w http.ResponseWriter, r *http.Request) {
	s.serveMediaImage(w, r, "backdrop")
}

func (s *Server) serveMediaImage(w http.ResponseWriter, r *http.Request, field string) {
	item, err := s.catalog.MediaForEntry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	ref := item.Icon
	if field == "backdrop" {
		ref = item.Backdrop
	}
	if id, ok := strings.CutPrefix(ref, "file:"); ok {
		if _, err := uuid.Parse(id); err != nil {
			http.NotFound(w, r)
			return
		}
		size := "medium"
		if field == "backdrop" {
			size = "large"
		}
		http.Redirect(w, r, "/api/v1/entries/"+id+"/thumbnail?size="+size, http.StatusFound)
		return
	}
	if key, ok := strings.CutPrefix(ref, "cache:posters/"); ok {
		base, extension, found := strings.Cut(key, ".")
		if !found {
			http.NotFound(w, r)
			return
		}
		filename, err := mediaengine.ArtifactPath(s.cacheDir, "posters", base, extension)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filename)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			s.internalError(w, err)
			return
		}
		mimeType := "image/jpeg"
		if extension == "png" {
			mimeType = "image/png"
		}
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+key+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, key, info.ModTime(), file)
		return
	}
	http.NotFound(w, r)
}

func writeThumbnailPending(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reprocess(entry model.Entry) {
	go func() {
		if err := s.indexer.ReprocessEntry(s.ctx, entry); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("reprocess entry %s: %v", entry.ID, err)
		}
	}()
}

func (s *Server) copyEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	var input struct {
		ParentID string  `json:"parentId"`
		Name     *string `json:"name"`
	}
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	name := entry.Name
	if input.Name != nil {
		name = *input.Name
	}
	if input.ParentID == "" || !validEntryName(name) {
		writeError(w, http.StatusBadRequest, "invalid_request", "parentId and a valid name are required")
		return
	}
	parent, err := s.catalog.Entry(r.Context(), input.ParentID)
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "destination directory not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if parent.Type != model.EntryDirectory {
		writeError(w, http.StatusBadRequest, "invalid_request", "destination is not a directory")
		return
	}
	if !entry.Available || !parent.Available {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	if entry.Type == model.EntryDirectory && entry.StorageID == parent.StorageID &&
		(parent.Path == entry.Path || strings.HasPrefix(parent.Path, entry.Path+"/")) {
		writeError(w, http.StatusBadRequest, "invalid_request", "a directory cannot be copied into itself")
		return
	}
	source, sourceOK := s.storages.Get(entry.StorageID)
	destination, destinationOK := s.storages.Get(parent.StorageID)
	if !sourceOK || !destinationOK {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	destinationPath := path.Join(parent.Path, name)
	createdRoot, err := copyStorageTree(r.Context(), source, destination, entry.Path, destinationPath)
	if err != nil {
		if createdRoot {
			_ = removeStorageTree(context.Background(), destination, destinationPath)
		}
		s.writeStorageError(w, err)
		return
	}
	created, err := s.catalogStorageTree(r.Context(), destination, parent.StorageID, destinationPath, &parent.ID)
	if err != nil {
		s.reconcile(parent.StorageID)
		s.internalError(w, fmt.Errorf("catalog copied entry: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, responseFor(*created))
}

func copyStorageTree(ctx context.Context, source, destination storage.Storage, sourcePath, destinationPath string) (bool, error) {
	info, err := source.Stat(ctx, sourcePath)
	if err != nil {
		return false, err
	}
	switch info.Type {
	case model.EntryFile:
		file, err := source.Open(ctx, sourcePath)
		if err != nil {
			return false, err
		}
		defer file.Close()
		_, err = destination.Create(ctx, destinationPath, file)
		return err == nil, err
	case model.EntryDirectory:
		if err := destination.Mkdir(ctx, destinationPath); err != nil {
			return false, err
		}
		children, err := source.ReadDir(ctx, sourcePath)
		if err != nil {
			return true, err
		}
		for _, child := range children {
			if _, err := copyStorageTree(ctx, source, destination, child.Path, path.Join(destinationPath, child.Name)); err != nil {
				return true, err
			}
		}
		return true, nil
	default:
		return false, storage.ErrUnsupported
	}
}

func removeStorageTree(ctx context.Context, backend storage.Storage, entryPath string) error {
	info, err := backend.Stat(ctx, entryPath)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Type == model.EntryDirectory {
		children, err := backend.ReadDir(ctx, entryPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := removeStorageTree(ctx, backend, child.Path); err != nil {
				return err
			}
		}
	}
	return backend.Remove(ctx, entryPath)
}

func (s *Server) catalogStorageTree(ctx context.Context, backend storage.Storage, storageID, entryPath string, parentID *string) (*model.Entry, error) {
	info, err := backend.Stat(ctx, entryPath)
	if err != nil {
		return nil, err
	}
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(info.Name)), ".")
	created, err := s.catalog.AddEntry(ctx, model.Entry{
		StorageID: storageID, ParentID: parentID, Name: info.Name, Path: entryPath, Type: info.Type,
		Size: info.Size, ModifiedAt: info.ModifiedAt, Inode: info.Inode, Device: info.Device,
		MIME: mime.TypeByExtension(path.Ext(info.Name)), Extension: extension,
	})
	if err != nil {
		return nil, err
	}
	if err := s.indexer.EnqueueEntries(ctx, []model.Entry{*created}); err != nil {
		s.logger.Printf("enqueue copied entry %s: %v", created.ID, err)
	}
	if info.Type == model.EntryDirectory {
		children, err := backend.ReadDir(ctx, entryPath)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if _, err := s.catalogStorageTree(ctx, backend, storageID, child.Path, &created.ID); err != nil {
				return nil, err
			}
		}
	}
	return created, nil
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	parent, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "parent entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if parent.Type != model.EntryDirectory {
		writeError(w, http.StatusBadRequest, "invalid_request", "parent entry is not a directory")
		return
	}
	if !parent.Available {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	backend, ok := s.storages.Get(parent.StorageID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "multipart/form-data upload is required")
		return
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_request", "upload must contain a file field")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart upload")
			return
		}
		name := part.FileName()
		if name == "" {
			_ = part.Close()
			continue
		}
		if !validEntryName(name) {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, "invalid_name", "filename must be a valid single path component")
			return
		}
		entryPath := path.Join(parent.Path, name)
		info, createErr := backend.Create(r.Context(), entryPath, part)
		_ = part.Close()
		if createErr != nil {
			s.writeStorageError(w, createErr)
			return
		}
		extension := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
		contentType := mime.TypeByExtension(path.Ext(name))
		created, err := s.catalog.AddEntry(r.Context(), model.Entry{
			StorageID: parent.StorageID, ParentID: &parent.ID, Name: name, Path: entryPath,
			Type: info.Type, Size: info.Size, ModifiedAt: info.ModifiedAt, Inode: info.Inode, Device: info.Device,
			MIME: contentType, Extension: extension,
		})
		if err != nil {
			s.reconcile(parent.StorageID)
			s.internalError(w, fmt.Errorf("catalog uploaded file: %w", err))
			return
		}
		if err := s.indexer.EnqueueEntries(r.Context(), []model.Entry{*created}); err != nil {
			s.logger.Printf("enqueue uploaded entry %s: %v", created.ID, err)
		}
		writeJSON(w, http.StatusCreated, responseFor(*created))
		return
	}
}

func (s *Server) createDirectory(w http.ResponseWriter, r *http.Request) {
	parent, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "parent entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if parent.Type != model.EntryDirectory {
		writeError(w, http.StatusBadRequest, "invalid_request", "parent entry is not a directory")
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	if !validEntryName(input.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "name must be a valid single path component")
		return
	}
	backend, ok := s.storages.Get(parent.StorageID)
	if !ok || !parent.Available {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	entryPath := path.Join(parent.Path, input.Name)
	if err := backend.Mkdir(r.Context(), entryPath); err != nil {
		s.writeStorageError(w, err)
		return
	}
	info, err := backend.Stat(r.Context(), entryPath)
	if err != nil {
		s.reconcile(parent.StorageID)
		s.internalError(w, fmt.Errorf("stat created directory: %w", err))
		return
	}
	created, err := s.catalog.AddEntry(r.Context(), model.Entry{
		StorageID: parent.StorageID, ParentID: &parent.ID, Name: input.Name, Path: entryPath,
		Type: info.Type, Size: info.Size, ModifiedAt: info.ModifiedAt, Inode: info.Inode, Device: info.Device,
	})
	if err != nil {
		s.reconcile(parent.StorageID)
		s.internalError(w, fmt.Errorf("catalog created directory: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, responseFor(*created))
}

func (s *Server) updateEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if entry.ParentID == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "storage root cannot be moved or renamed")
		return
	}
	var input struct {
		Name     *string `json:"name"`
		ParentID *string `json:"parentId"`
	}
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	name := entry.Name
	if input.Name != nil {
		name = *input.Name
	}
	if !validEntryName(name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "name must be a valid single path component")
		return
	}
	parentID := *entry.ParentID
	if input.ParentID != nil {
		parentID = *input.ParentID
	}
	if parentID == entry.ID || parentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid destination directory")
		return
	}
	parent, err := s.catalog.Entry(r.Context(), parentID)
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "destination directory not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if parent.Type != model.EntryDirectory || parent.StorageID != entry.StorageID {
		writeError(w, http.StatusBadRequest, "invalid_request", "destination must be a directory in the same storage")
		return
	}
	if entry.Type == model.EntryDirectory && (parent.Path == entry.Path || strings.HasPrefix(parent.Path, entry.Path+"/")) {
		writeError(w, http.StatusBadRequest, "invalid_request", "a directory cannot be moved into itself")
		return
	}
	newPath := path.Join(parent.Path, name)
	if newPath == entry.Path {
		writeJSON(w, http.StatusOK, responseFor(*entry))
		return
	}
	backend, ok := s.storages.Get(entry.StorageID)
	if !ok || !entry.Available || !parent.Available {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	if err := backend.Rename(r.Context(), entry.Path, newPath); err != nil {
		s.writeStorageError(w, err)
		return
	}
	updated, err := s.catalog.MoveEntry(r.Context(), entry.ID, parent.ID, name, newPath)
	if err != nil {
		s.reconcile(entry.StorageID)
		s.internalError(w, fmt.Errorf("catalog moved entry: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, responseFor(*updated))
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if entry.ParentID == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "storage root cannot be deleted")
		return
	}
	backend, ok := s.storages.Get(entry.StorageID)
	if !ok || !entry.Available {
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
		return
	}
	if err := backend.Remove(r.Context(), entry.Path); err != nil {
		s.writeStorageError(w, err)
		return
	}
	if err := s.catalog.DeleteEntry(r.Context(), entry.ID); err != nil {
		s.reconcile(entry.StorageID)
		s.internalError(w, fmt.Errorf("catalog deleted entry: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) &&
		strings.TrimSpace(name) != "" && !strings.ContainsAny(name, `/\\`) && !strings.ContainsRune(name, 0)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body")
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return errors.New("additional JSON value")
	}
	return nil
}

func (s *Server) writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "entry_exists", "an entry with that name already exists")
	case errors.Is(err, storage.ErrNotEmpty):
		writeError(w, http.StatusConflict, "directory_not_empty", "directory is not empty")
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
	case errors.Is(err, storage.ErrOffline):
		writeError(w, http.StatusServiceUnavailable, "storage_offline", "storage is offline")
	case errors.Is(err, storage.ErrPathTraversal):
		writeError(w, http.StatusBadRequest, "invalid_path", "invalid storage path")
	case errors.Is(err, storage.ErrUnsupported):
		writeError(w, http.StatusBadRequest, "unsupported_entry", "entry type is not supported for this operation")
	default:
		s.internalError(w, err)
	}
}

func (s *Server) reconcile(storageID string) {
	go func() {
		if err := s.indexer.Scan(s.ctx, storageID); err != nil && !errors.Is(err, indexer.ErrScanInProgress) && !errors.Is(err, context.Canceled) {
			s.logger.Printf("reconcile storage %s: %v", storageID, err)
		}
	}()
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
	result, err := s.responsesFor(r.Context(), items)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": "0.4.0", "features": map[string]bool{"media": true, "mediaEnhancement": true, "thumbnail": true, "poster": true, "directPlay": true, "remux": true, "hls": true, "transcode": true, "continueWatching": true, "scanProgress": true}})
}

func (s *Server) systemStatus(w http.ResponseWriter, r *http.Request) {
	stats := jobs.Stats{}
	var err error
	if s.jobQueue != nil {
		stats, err = s.jobQueue.Stats(r.Context(), processor.JobProcessEntry)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"processing": stats})
}

func (s *Server) systemFailures(w http.ResponseWriter, r *http.Request) {
	if s.jobQueue == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []jobs.FailureGroup{}})
		return
	}
	groups, err := s.jobQueue.FailureGroups(r.Context(), processor.JobProcessEntry, 30)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": groups})
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
	if !s.indexer.CanScanStorage(id) {
		writeError(w, http.StatusBadRequest, "no_library_sources", "storage has no configured library sources")
		return
	}
	go func() {
		if err := s.indexer.Scan(s.ctx, id); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, indexer.ErrScanInProgress) {
			s.logger.Printf("scan storage %s: %v", id, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scanning"})
}

func (s *Server) scanEntrySubtree(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !s.indexer.CanScanSubtree(entry.StorageID, entry.Path) {
		writeError(w, http.StatusBadRequest, "invalid_scan_scope", "select an entry inside a configured library")
		return
	}
	if entry.Type == model.EntryFile {
		if !entry.Available {
			writeError(w, http.StatusConflict, "entry_unavailable", "file is not available for processing")
			return
		}
		if err := s.indexer.ReprocessEntry(r.Context(), *entry); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued", "scope": "file"})
		return
	}
	if entry.Type != model.EntryDirectory {
		writeError(w, http.StatusBadRequest, "invalid_scan_scope", "select a file or directory inside a configured library")
		return
	}
	if s.indexer.IsScanning(entry.StorageID) {
		writeError(w, http.StatusConflict, "scan_in_progress", "storage scan already in progress")
		return
	}
	go func() {
		if err := s.indexer.ScanSubtree(s.ctx, entry.StorageID, entry.Path); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, indexer.ErrScanInProgress) {
			s.logger.Printf("scan directory %s: %v", entry.Path, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scanning", "scope": "subtree"})
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
	Media      *mediaResponse    `json:"media,omitempty"`
}

type mediaResponse struct {
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Icon        string          `json:"icon,omitempty"`
	Backdrop    string          `json:"backdrop,omitempty"`
	Year        *int            `json:"year,omitempty"`
	Line1       string          `json:"line1,omitempty"`
	Line2       string          `json:"line2,omitempty"`
	Line3       string          `json:"line3,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	MatchLocked bool            `json:"matchLocked,omitempty"`
}

func mediaView(item model.Media) mediaResponse {
	view := mediaResponse{Kind: item.Kind, Title: item.Title, Year: item.Year,
		Line1: item.Line1, Line2: item.Line2, Line3: item.Line3,
		Data: item.Data, MatchLocked: item.MatchLocked}
	if item.Icon != "" {
		view.Icon = "/api/v1/entries/" + item.FileID + "/icon"
	}
	if item.Backdrop != "" {
		view.Backdrop = "/api/v1/entries/" + item.FileID + "/backdrop"
	}
	return view
}

func (s *Server) responsesFor(ctx context.Context, entries []model.Entry) ([]entryResponse, error) {
	entryIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryIDs = append(entryIDs, entry.ID)
	}
	media, err := s.catalog.MediasForEntries(ctx, entryIDs)
	if err != nil {
		return nil, err
	}
	result := make([]entryResponse, len(entries))
	for index, entry := range entries {
		result[index] = responseFor(entry)
		if item, ok := media[entry.ID]; ok {
			view := mediaView(item)
			result[index].Media = &view
			if view.Icon != "" {
				result[index].Links["thumbnail"] = view.Icon
			}
		}
	}
	return result, nil
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
		links["media"] = "/api/v1/entries/" + entry.ID + "/media"
		if hasThumbnailExtension(entry.Extension) {
			links["thumbnail"] = "/api/v1/entries/" + entry.ID + "/thumbnail?size=medium"
		}
	}
	return entryResponse{ID: entry.ID, ParentID: entry.ParentID, Name: entry.Name, Type: entry.Type,
		Size: entry.Size, MIME: entry.MIME, Extension: entry.Extension, Available: entry.Available,
		ModifiedAt: modified, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt, Links: links}
}

func hasThumbnailExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case "jpg", "jpeg", "png", "gif", "epub", "pdf", "mp4", "m4v", "mkv", "webm", "mov", "avi", "mpeg", "mpg", "ts", "m2ts", "flv", "wmv", "rmvb", "mp3", "m4a", "aac", "flac", "ogg", "opus", "wma", "aiff", "ape":
		return true
	default:
		return false
	}
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
	result, err := s.responsesFor(r.Context(), []model.Entry{*entry})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result[0])
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
	result, err := s.responsesFor(r.Context(), items)
	if err != nil {
		s.internalError(w, err)
		return
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
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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

const maxTextPreviewSize = 1 << 20

func (s *Server) text(w http.ResponseWriter, r *http.Request) {
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
	if entry.Size > maxTextPreviewSize {
		writeError(w, http.StatusRequestEntityTooLarge, "preview_too_large", "text preview is limited to 1 MiB")
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
		s.internalError(w, fmt.Errorf("open text entry %s: %w", entry.ID, err))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTextPreviewSize+1))
	if err != nil {
		s.internalError(w, fmt.Errorf("read text entry %s: %w", entry.ID, err))
		return
	}
	if len(data) > maxTextPreviewSize {
		writeError(w, http.StatusRequestEntityTooLarge, "preview_too_large", "text preview is limited to 1 MiB")
		return
	}
	text, ok := decodeText(data)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "binary_file", "file is not recognized as text")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, text)
}

func decodeText(data []byte) (string, bool) {
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		littleEndian := data[0] == 0xff
		data = data[2:]
		if len(data)%2 != 0 {
			return "", false
		}
		units := make([]uint16, len(data)/2)
		for index := range units {
			if littleEndian {
				units[index] = binary.LittleEndian.Uint16(data[index*2:])
			} else {
				units[index] = binary.BigEndian.Uint16(data[index*2:])
			}
		}
		return string(utf16.Decode(units)), true
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
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
