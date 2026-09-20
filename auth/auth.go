package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

type Role int

const (
	Viewer Role = iota + 1
	Editor
	Admin
)

type Principal struct {
	Name string
	Role Role
}

type Token struct {
	Name   string
	Secret string
	Role   Role
}

type tokenKey [32]byte

type principalKey struct{}

func ParseRole(value string) (Role, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "viewer":
		return Viewer, true
	case "editor":
		return Editor, true
	case "admin":
		return Admin, true
	default:
		return 0, false
	}
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func Middleware(tokens []Token) func(http.Handler) http.Handler {
	configured := make(map[tokenKey]Principal, len(tokens))
	for _, token := range tokens {
		if token.Secret == "" || token.Role == 0 {
			continue
		}
		configured[sha256.Sum256([]byte(token.Secret))] = Principal{Name: token.Name, Role: token.Role}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := authenticate(r, configured)
			if !ok {
				w.Header().Add("WWW-Authenticate", `Bearer realm="files-go"`)
				w.Header().Add("WWW-Authenticate", `Basic realm="files-go"`)
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			if principal.Role < requiredRole(r) {
				writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
		})
	}
}

func authenticate(r *http.Request, configured map[tokenKey]Principal) (Principal, bool) {
	if len(configured) == 0 {
		if isLoopback(r.RemoteAddr) {
			return Principal{Name: "local", Role: Admin}, true
		}
		return Principal{}, false
	}
	secret := ""
	if value := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(value), "bearer ") {
		secret = strings.TrimSpace(value[len("Bearer "):])
	} else if _, password, ok := r.BasicAuth(); ok {
		secret = password
	}
	if secret == "" {
		return Principal{}, false
	}
	principal, ok := configured[sha256.Sum256([]byte(secret))]
	return principal, ok
}

func requiredRole(r *http.Request) Role {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return Viewer
	}
	if strings.HasSuffix(r.URL.Path, "/scan") {
		return Admin
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/playback/") {
		return Viewer
	}
	return Editor
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
