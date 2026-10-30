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
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/hls"
	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/store"
)

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      *config.Config
	store    *store.Store
	scanner  *store.Scanner
	pipeline *media.Pipeline
	tmdb     *media.TMDB
	tpdb     *theporndb.Client
	thumbs   *Thumbs
	hls      *hls.Manager
	vaapi    string // render device when hardware transcoding works

	statsMu       sync.Mutex
	stats         store.Stats
	statsAt       time.Time
	subtitleJobs  jobs
	subtitleSlots chan struct{}
}

func New(ctx context.Context, cfg *config.Config, st *store.Store, scanner *store.Scanner, pipeline *media.Pipeline, client *media.TMDB, tpdb *theporndb.Client) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	streams, err := hls.New(ctx, hls.Config{CacheDir: filepath.Join(cfg.Data, "cache", "hls"), FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe})
	if err != nil {
		cancel()
		return nil, err
	}
	return &Server{cancel: cancel, ctx: ctx, cfg: cfg, store: st, scanner: scanner, pipeline: pipeline, tmdb: client, tpdb: tpdb, subtitleSlots: make(chan struct{}, 2),
		thumbs: NewThumbs(ctx, cfg, st, scanner, client),
		hls:    streams,
		vaapi:  detectVAAPI(cfg.FFmpeg, cfg.HWAccel, cfg.HWDevice)}, nil
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
	mux.HandleFunc("GET /api/files/{id}/play", s.play)
	mux.HandleFunc("GET /api/hls/{key}/{name}", s.segment)
	mux.HandleFunc("DELETE /api/hls/{key}", s.releasePlayback)
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
		// HLS segments are addressed by an unguessable session key; native
		// players (iOS) do not reliably resend the page's credentials.
		if strings.HasPrefix(r.URL.Path, "/api/hls/") {
			next.ServeHTTP(w, r)
			return
		}
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
	Version int64  `json:"version"`
	Thumb   bool   `json:"thumb,omitempty"`
	Play    string `json:"play,omitempty"` // direct or stream
}

func (s *Server) view(f store.File) View {
	m := meta(f)
	v := View{File: f, Version: f.Revision, Thumb: s.thumbs.Has(f, m)}
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
	// Browsing reads only the index; the background scanner handles disk changes.
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
	// A folder is resynced with the disk first, then re-identified itself.
	if f.Dir {
		if err := s.scanner.SyncDir(r.Context(), f); err != nil {
			s.fail(w, err)
			return
		}
		files, err := s.store.MediaUnder(r.Context(), f)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, child := range files {
			if err := s.store.Invalidate(r.Context(), child.ID); err != nil {
				s.fail(w, err)
				return
			}
		}
		s.pipeline.Wake()
		f, _ = s.store.Get(r.Context(), f.ID)
	}
	if err := s.store.Invalidate(r.Context(), f.ID); err != nil {
		s.fail(w, err)
		return
	}
	fresh, err := s.store.Get(r.Context(), f.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	f = fresh
	if err := s.pipeline.Rematch(r.Context(), f); err != nil && !errors.Is(err, store.ErrStale) {
		s.fail(w, err)
		return
	}
	f, err = s.store.Get(r.Context(), f.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
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
	file, err := s.scanner.Open(r.Context(), f)
	if err != nil {
		s.fail(w, err)
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

// Candidate exposes a provider-independent selection; artwork is already a URL.
type Candidate struct {
	Match    store.Match `json:"match"`
	Title    string      `json:"title"`
	Original string      `json:"original,omitempty"`
	Year     int         `json:"year,omitempty"`
	Overview string      `json:"overview,omitempty"`
	Poster   string      `json:"poster,omitempty"`
}

func (s *Server) candidates(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	query, provider, kind := q.Get("q"), q.Get("provider"), q.Get("type")
	m := meta(f)
	if query == "" {
		query = first(m.Info["Code"], m.Show, m.Title, media.ParseName(f.Name).Title)
	}
	if provider == "" {
		provider = "tmdb"
		root, _ := s.scanner.Root(f.Root)
		hint, _ := root.TypeOf(f.Path)
		if hint == "jav" {
			provider = "theporndb"
		}
		if m.Match != nil {
			provider = m.Match.Provider
		}
	}
	items := []Candidate{}
	switch provider {
	case "tmdb":
		if s.tmdb == nil {
			writeError(w, http.StatusServiceUnavailable, "TMDB is not configured")
			return
		}
		if kind == "" {
			kind = "movie"
			if m.Kind == "episode" || m.Kind == "show" || m.Kind == "season" {
				kind = "tv"
			}
		}
		if kind != "movie" && kind != "tv" {
			writeError(w, 400, "invalid TMDB type")
			return
		}
		found, err := s.tmdb.Search(r.Context(), kind, query, 0)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		for _, it := range found {
			poster := ""
			if it.Poster != "" {
				poster = "https://image.tmdb.org/t/p/w92" + it.Poster
			}
			items = append(items, Candidate{Match: store.Match{Provider: provider, Type: it.Type, ID: strconv.Itoa(it.ID)}, Title: it.Title, Original: it.Original, Year: it.Year, Overview: it.Overview, Poster: poster})
		}
	case "theporndb":
		if s.tpdb == nil {
			writeError(w, 503, "ThePornDB is not configured")
			return
		}
		if kind != "" && kind != "jav" {
			writeError(w, 400, "invalid ThePornDB type")
			return
		}
		found, err := s.tpdb.SearchJAV(r.Context(), url.Values{"q": {query}, "per_page": {"50"}})
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		for _, it := range found.Data {
			poster := ""
			if it.Poster != nil {
				poster = *it.Poster
			}
			year, _ := strconv.Atoi(strings.Split(it.Date, "-")[0])
			items = append(items, Candidate{Match: store.Match{Provider: provider, Type: "jav", ID: it.ID}, Title: it.Title, Year: year, Overview: it.Description, Poster: poster})
		}
	default:
		writeError(w, 400, "invalid catalog provider")
		return
	}
	scope, err := s.pipeline.MatchScope(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"items": items, "scope": map[string]any{"id": scope.ID, "name": scope.Name, "dir": scope.Dir}})
}

// match persists a shared work's manual choice before scheduling enrichment.
// Directory selections apply to their files; the directory only aggregates.
func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	f, ok := s.lookup(w, r)
	if !ok {
		return
	}
	scope, err := s.pipeline.MatchScope(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	var choice *store.Match
	if r.Method == http.MethodPut {
		var body struct {
			Match store.Match `json:"match"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || media.ValidateMatch(body.Match) != nil {
			writeError(w, 400, `expected {"match":{"provider":"tmdb|theporndb","type":"movie|tv|jav","id":"..."}}`)
			return
		}
		shared := true
		if scope.Dir {
			if shared, err = s.pipeline.SharedWork(r.Context(), scope); err != nil {
				s.fail(w, err)
				return
			}
		}
		if (!f.Dir && f.Kind != "video") || scope.Path == "" || !shared {
			writeError(w, 400, "select a media file or an identified work directory")
			return
		}
		choice = &body.Match
	}
	if err := s.store.SetMatch(r.Context(), scope, choice); err != nil {
		s.fail(w, err)
		return
	}
	s.pipeline.Wake()
	f, err = s.store.Get(r.Context(), f.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.pipeline.Rematch(r.Context(), f); err != nil && !errors.Is(err, store.ErrStale) {
		s.fail(w, err)
		return
	}
	f, err = s.store.Get(r.Context(), f.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
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

// Close stops and joins shared media jobs before the index is closed.
func (s *Server) Close() error {
	s.cancel()
	s.subtitleJobs.close()
	s.thumbs.work.close()
	return s.hls.Close()
}
