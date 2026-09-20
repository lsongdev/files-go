package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
)

func (s *Server) getEntryMediaV2(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.MediaForEntry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		if r.URL.Query().Get("optional") == "1" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusNotFound, "media_not_found", "entry has no media enhancement")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mediaView(*item))
}

func (s *Server) mediaQueryType(r *http.Request, entry model.Entry) (string, error) {
	types, err := s.catalog.LibraryTypesForEntry(r.Context(), entry)
	if err != nil {
		return "", err
	}
	for _, kind := range types {
		if kind == "tv" {
			return "tv", nil
		}
	}
	return "movie", nil
}

func (s *Server) searchMediaCandidatesV2(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		writeError(w, http.StatusServiceUnavailable, "media_provider_unavailable", "media metadata provider is not configured")
		return
	}
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "q must not exceed 200 characters")
		return
	}
	parsed := mediaengine.ParseName(entry.Name)
	if query == "" {
		query = parsed.Title
	}
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	kind, err := s.mediaQueryType(r, *entry)
	if err != nil {
		s.internalError(w, err)
		return
	}
	results, err := s.provider.Search(r.Context(), mediaengine.Query{Type: kind, Title: query, Year: parsed.Year, Language: s.language})
	if err != nil {
		s.logger.Printf("search media candidates %s: %v", entry.ID, err)
		writeError(w, http.StatusBadGateway, "media_provider_error", "media metadata provider request failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func (s *Server) setEntryMediaV2(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		writeError(w, http.StatusServiceUnavailable, "media_provider_unavailable", "media metadata provider is not configured")
		return
	}
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
		CandidateID   string `json:"candidateId"`
		CandidateType string `json:"candidateType"`
	}
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	if input.CandidateID == "" || (input.CandidateType != "movie" && input.CandidateType != "tv") {
		writeError(w, http.StatusBadRequest, "invalid_request", "candidateId and movie/tv candidateType are required")
		return
	}
	item, err := s.provider.Fetch(r.Context(), input.CandidateType, input.CandidateID, s.language)
	if err != nil {
		s.logger.Printf("fetch manual media candidate %s: %v", entry.ID, err)
		writeError(w, http.StatusBadGateway, "media_provider_error", "media metadata provider request failed")
		return
	}
	kind := input.CandidateType
	if entry.Type == model.EntryFile && kind == "tv" {
		parsed := mediaengine.ParseName(entry.Name)
		if parsed.Season != nil && parsed.Episode != nil {
			kind = "episode"
		}
	}
	data, err := json.Marshal(item)
	if err != nil {
		s.internalError(w, err)
		return
	}
	candidate := catalog.MediaCandidate{Kind: kind, Title: item.Title, Year: item.Year, Data: data}
	resolved, err := s.catalog.SetMediaCandidate(r.Context(), entry.ID, "manual", candidate)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if s.artwork != nil {
		go func() {
			if err := s.artwork.ProcessEntry(s.ctx, entry.ID); err != nil && s.ctx.Err() == nil {
				s.logger.Printf("manual artwork %s: %v", entry.ID, err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, mediaView(*resolved))
}

func (s *Server) unmatchEntryMediaV2(w http.ResponseWriter, r *http.Request) {
	if _, err := s.catalog.SetMediaCandidates(r.Context(), r.PathValue("id"), map[string]*catalog.MediaCandidate{
		"manual": {}, "tmdb": nil,
	}); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rematchEntryMediaV2(w http.ResponseWriter, r *http.Request) {
	entry, err := s.catalog.Entry(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry_not_found", "entry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := s.catalog.SetMediaCandidates(r.Context(), entry.ID, map[string]*catalog.MediaCandidate{"manual": nil, "tmdb": nil}); err != nil {
		s.internalError(w, err)
		return
	}
	s.reprocess(*entry)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "matching"})
}
