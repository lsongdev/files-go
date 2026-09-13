package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"
)

//go:embed index.html app.js styles.css
var files embed.FS

var builtAt = time.Now().UTC()
var assetVersion = func() string {
	app, _ := files.ReadFile("app.js")
	styles, _ := files.ReadFile("styles.css")
	hash := sha256.New()
	_, _ = hash.Write(app)
	_, _ = hash.Write(styles)
	return hex.EncodeToString(hash.Sum(nil)[:8])
}()

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(r.URL.Path)
		if name == "." || name == "/" {
			name = "index.html"
		} else {
			name = name[1:]
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			name = "index.html"
			data, err = fs.ReadFile(files, name)
		}
		if name == "index.html" {
			data = bytes.ReplaceAll(data, []byte("{{ASSET_VERSION}}"), []byte(assetVersion))
		}
		if err != nil {
			http.Error(w, "web interface unavailable", http.StatusInternalServerError)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if name == "index.html" {
			// The UI is a long-lived SPA. Never let a browser keep an old HTML
			// shell across server upgrades, otherwise it can continue running a
			// stale app.js while API responses already use the new version.
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com; connect-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, builtAt, bytes.NewReader(data))
	})
}
