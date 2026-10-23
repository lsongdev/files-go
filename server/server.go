// Package server exposes the index over HTTP. It never writes to indexed
// directories: files are opened read-only and there are no mutating routes.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/store"
)

type Server struct {
	ctx      context.Context
	cfg      *config.Config
	store    *store.Store
	scanner  *store.Scanner
	pipeline *media.Pipeline
	tmdb     *media.TMDB
	thumbs   *Thumbs
	streams  chan struct{}
	vaapi    string // render device when hardware transcoding works

	statsMu sync.Mutex
	stats   store.Stats
	statsAt time.Time
}

func New(ctx context.Context, cfg *config.Config, st *store.Store, scanner *store.Scanner, pipeline *media.Pipeline, client *media.TMDB) *Server {
	return &Server{ctx: ctx, cfg: cfg, store: st, scanner: scanner, pipeline: pipeline, tmdb: client,
		thumbs:  NewThumbs(cfg, st, scanner, client),
		streams: make(chan struct{}, 2),
		vaapi:   detectVAAPI(cfg.FFmpeg, cfg.HWAccel, cfg.HWDevice)}
}

// Handler serves the API and wraps everything (including web) in auth.
func (s *Server) Handler(web http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/roots", s.roots)
	mux.HandleFunc("POST /api/roots/{root}/scan", s.scan)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/issues", s.issues)
	mux.HandleFunc("GET /api/files/{id}", s.file)
	mux.HandleFunc("GET /api/files/{id}/children", s.children)
	mux.HandleFunc("POST /api/files/{id}/refresh", s.refresh)
	mux.HandleFunc("GET /api/files/{id}/raw", s.raw)
	mux.HandleFunc("GET /api/files/{id}/thumb", s.thumb)
	mux.HandleFunc("GET /api/files/{id}/backdrop", s.thumb)
	mux.HandleFunc("GET /api/files/{id}/stream", s.stream)
	mux.HandleFunc("GET /api/files/{id}/subtitles", s.subtitles)
	mux.HandleFunc("GET /api/files/{id}/subtitles/{track}", s.subtitle)
	mux.HandleFunc("GET /api/files/{id}/match", s.candidates)
	mux.HandleFunc("PUT /api/files/{id}/match", s.match)
	mux.HandleFunc("DELETE /api/files/{id}/match", s.match)
	mux.Handle("/", web)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	if s.cfg.Auth.Password == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Auth.Password)) != 1 ||
			s.cfg.Auth.Username != "" && user != s.cfg.Auth.Username {
			w.Header().Set("WWW-Authenticate", `Basic realm="files", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// View is a file as returned by the API.
type View struct {
	store.File
	Thumb bool   `json:"thumb,omitempty"`
	Play  string `json:"play,omitempty"` // direct or stream
}

func (s *Server) view(f store.File) View {
	m := meta(f)
	v := View{File: f, Thumb: s.thumbs.Has(f, m)}
	if f.MetaErr != "" && len(f.MetaErr) > 300 {
		v.MetaErr = f.MetaErr[:300]
	}
	switch f.Kind {
	case "video":
		v.Play = "stream"
		if directVideo(f, m) {
			v.Play = "direct"
		}
	case "audio":
		v.Play = "stream"
		switch media.Ext(f.Name) {
		case "mp3", "m4a", "m4b", "aac", "flac", "wav", "ogg", "oga", "opus":
			v.Play = "direct"
		}
	}
	return v
}

func (s *Server) views(files []store.File) []View {
	views := make([]View, len(files))
	for i, f := range files {
		views[i] = s.view(f)
	}
	return views
}

func meta(f store.File) media.Meta {
	var m media.Meta
	if len(f.Meta) > 0 {
		_ = json.Unmarshal(f.Meta, &m)
	}
	return m
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (store.File, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return store.File{}, false
	}
	f, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "file not found")
		return f, false
	}
	if err != nil {
		s.fail(w, err)
		return f, false
	}
	return f, true
}

func (s *Server) roots(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.scanner.Status())
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	root := r.PathValue("root")
	if _, ok := s.scanner.Root(root); !ok {
		writeError(w, http.StatusNotFound, "root not found")
		return
	}
	force := r.URL.Query().Get("force") == "1" // the root really is empty
	go func() {
		if err := s.scanner.Scan(s.ctx, root, force); err != nil {
			log.Printf("scan %s: %v", root, err)
		}
		s.pipeline.Wake()
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if time.Since(s.statsAt) > 5*time.Second {
		stats, err := s.store.Stats(r.Context(), s.pipeline.Version)
		if err != nil {
			s.fail(w, err)
			return
		}
		s.stats, s.statsAt = stats, time.Now()
	}
	writeJSON(w, map[string]any{"roots": s.scanner.Status(), "stats": s.stats})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	files, err := s.store.Search(r.Context(), store.SearchOptions{Query: q.Get("q"), Root: q.Get("root"), Kind: q.Get("kind"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"items": s.views(files)})
}

// issues lists files to review (?type=review) or that failed enrichment.
func (s *Server) issues(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	files, err := s.store.Issues(r.Context(), r.URL.Query().Get("type"), max(offset, 0), limit+1)
	if err != nil {
		s.fail(w, err)
		return
	}
	more := len(files) > limit
	if more {
		files = files[:limit]
	}
	writeJSON(w, map[string]any{"items": s.views(files), "more": more})
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ancestors, err := s.store.Ancestors(r.Context(), f.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if f.MetaVer != s.pipeline.Version {
		s.pipeline.Prioritize([]store.File{f})
	}
	writeJSON(w, map[string]any{"file": s.view(f), "ancestors": s.views(ancestors)})
}

func (s *Server) children(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if !dir.Dir {
		writeError(w, http.StatusBadRequest, "not a directory")
		return
	}
	// Browsing is served from the index; one stat detects whether this
	// level changed on disk since it was indexed.
	if s.scanner.Stale(dir) {
		if err := s.scanner.SyncDir(r.Context(), dir); err != nil {
			log.Printf("sync %s/%s: %v", dir.Root, dir.Path, err)
		}
		s.pipeline.Wake()
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	files, err := s.store.Children(r.Context(), dir.ID, max(offset, 0), limit+1)
	if err != nil {
		s.fail(w, err)
		return
	}
	more := len(files) > limit
	if more {
		files = files[:limit]
	}
	s.pipeline.Prioritize(files)
	writeJSON(w, map[string]any{"items": s.views(files), "more": more})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var err error
	if f.Dir {
		err = s.scanner.SyncDir(r.Context(), f)
		s.pipeline.Wake()
	} else {
		err = s.pipeline.Rematch(r.Context(), f)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, _ = s.store.Get(r.Context(), f.ID)
	writeJSON(w, s.view(f))
}

func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if f.Dir {
		writeError(w, http.StatusBadRequest, "not a file")
		return
	}
	name, err := s.scanner.Abs(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	file, err := os.Open(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "file is unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "file is unavailable")
		return
	}
	contentType := mime.TypeByExtension("." + media.Ext(f.Name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if r.URL.Query().Has("download") {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	if contentType != "application/pdf" { // the browser PDF viewer refuses sandboxed documents
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, f.Name, info.ModTime(), file)
}

func (s *Server) candidates(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if s.tmdb == nil {
		writeError(w, http.StatusServiceUnavailable, "TMDB is not configured")
		return
	}
	query, kind := r.URL.Query().Get("q"), r.URL.Query().Get("type")
	m := meta(f)
	if query == "" {
		query = first(m.Show, m.Title, media.ParseName(f.Name).Title)
	}
	if kind != "tv" {
		kind = "movie"
	}
	items, err := s.tmdb.Search(r.Context(), kind, query, 0)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

// match locks a file to a TMDB id (PUT {"tmdb":"movie/123"}) or removes
// the lock (DELETE), then re-enriches it immediately.
func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	m := meta(f)
	m.Locked, m.TMDB = false, ""
	if r.Method == http.MethodPut {
		var body struct{ TMDB string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.HasPrefix(body.TMDB, "movie/") && !strings.HasPrefix(body.TMDB, "tv/") {
			writeError(w, http.StatusBadRequest, `expected {"tmdb": "movie/<id>"} or "tv/<id>"`)
			return
		}
		m.Locked, m.TMDB = true, body.TMDB
	}
	f.Meta, _ = json.Marshal(m)
	if err := s.pipeline.Rematch(r.Context(), f); err != nil {
		s.fail(w, err)
		return
	}
	f, _ = s.store.Get(r.Context(), f.ID)
	writeJSON(w, s.view(f))
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("http: %v", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
