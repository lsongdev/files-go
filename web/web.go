package web

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"
)

//go:embed index.html app.js styles.css
var files embed.FS

var builtAt = time.Now().UTC()

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
		if err != nil {
			http.Error(w, "web interface unavailable", http.StatusInternalServerError)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com; connect-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, builtAt, bytes.NewReader(data))
	})
}
