// Package web embeds the browser UI.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed index.html app.js player.js styles.css vendor/*
//go:embed node_modules/@lsongdev/scripts/react/react.js node_modules/@lsongdev/scripts/react/preact.js node_modules/@lsongdev/scripts/react/preact-hooks.js node_modules/@lsongdev/scripts/react/htm.js
//go:embed node_modules/@lsongdev/scripts/react/components/dialog.js node_modules/@lsongdev/scripts/dom/dialog.js
//go:embed node_modules/@lsongdev/scripts/LICENSE node_modules/@lsongdev/scripts/THIRD_PARTY_NOTICES.md
//go:embed node_modules/@lsongdev/stylesheets/dialog.css node_modules/@lsongdev/stylesheets/base.css node_modules/@lsongdev/stylesheets/color.css
var files embed.FS

var started = time.Now()

// version busts browser caches when the embedded assets change.
var version = func() string {
	h := sha256.New()
	for _, name := range []string{"app.js", "player.js", "styles.css",
		"node_modules/@lsongdev/scripts/react/react.js",
		"node_modules/@lsongdev/scripts/react/preact.js",
		"node_modules/@lsongdev/scripts/react/preact-hooks.js",
		"node_modules/@lsongdev/scripts/react/htm.js",
		"node_modules/@lsongdev/scripts/react/components/dialog.js",
		"node_modules/@lsongdev/scripts/dom/dialog.js",
		"node_modules/@lsongdev/stylesheets/dialog.css",
		"node_modules/@lsongdev/stylesheets/base.css",
		"node_modules/@lsongdev/stylesheets/color.css",
	} {
		data, _ := files.ReadFile(name)
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}()

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://image.tmdb.org https://thumb.theporndb.net https://cdn.theporndb.net; media-src 'self' blob:; frame-src 'self'; object-src 'none'; base-uri 'none'"

// Handler serves static assets and falls back to index.html for app routes.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(r.URL.Path)[1:]
		data, err := files.ReadFile(name)
		if err != nil || name == "" {
			name = "index.html"
			data, _ = files.ReadFile(name)
			data = bytes.ReplaceAll(data, []byte("{{VERSION}}"), []byte(version))
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			if strings.HasPrefix(name, "node_modules/") {
				// Relative module imports keep their package paths, so revalidate on reload.
				w.Header().Set("Cache-Control", "no-cache")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, started, bytes.NewReader(data))
	})
}
