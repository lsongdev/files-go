package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareDefaultsToLoopbackOnly(t *testing.T) {
	handler := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.Role != Admin {
			t.Fatalf("principal = %#v, %v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	local := httptest.NewRequest(http.MethodGet, "http://example/api/v1/libraries", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, local)
	if response.Code != http.StatusNoContent {
		t.Fatalf("loopback status = %d", response.Code)
	}
	remote := httptest.NewRequest(http.MethodGet, "http://example/api/v1/libraries", nil)
	remote.RemoteAddr = "192.0.2.10:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, remote)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("remote status = %d, want 401", response.Code)
	}
}

func TestMiddlewareEnforcesRoles(t *testing.T) {
	handler := Middleware([]Token{
		{Name: "reader", Secret: "read-token", Role: Viewer},
		{Name: "writer", Secret: "write-token", Role: Editor},
		{Name: "admin", Secret: "admin-token", Role: Admin},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	tests := []struct {
		method, path, token string
		want                int
	}{
		{http.MethodGet, "/api/v1/entries/1", "read-token", http.StatusNoContent},
		{http.MethodDelete, "/api/v1/entries/1", "read-token", http.StatusForbidden},
		{http.MethodDelete, "/api/v1/entries/1", "write-token", http.StatusNoContent},
		{http.MethodPost, "/api/v1/storages/disk/scan", "write-token", http.StatusForbidden},
		{http.MethodPost, "/api/v1/storages/disk/scan", "admin-token", http.StatusNoContent},
		{http.MethodPost, "/api/v1/playback/1", "read-token", http.StatusNoContent},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, "http://example"+tt.path, nil)
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("Authorization", "Bearer "+tt.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, w.Code, tt.want)
		}
	}
}
