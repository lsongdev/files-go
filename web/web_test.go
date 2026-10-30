package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOfflineScriptsAndPinnedVendorManifest(t *testing.T) {
	var manifest map[string]struct{ URL, SHA256 string }
	data, e := files.ReadFile("vendor/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &manifest); e != nil {
		t.Fatal(e)
	}
	for name, entry := range manifest {
		data, e := files.ReadFile("vendor/" + name)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			t.Fatalf("vendor hash changed: %s", name)
		}
	}
	for _, path := range []string{"/app.js", "/player.js", "/vendor/hls-1.6.13.mjs"} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Header().Get("Content-Security-Policy"), "unpkg.com") {
			t.Fatalf("offline asset: %s %d", path, w.Code)
		}
	}
	for _, name := range []string{"app.js", "player.js"} {
		b, _ := files.ReadFile(name)
		if strings.Contains(string(b), "https://unpkg.com") {
			t.Fatalf("remote script remains: %s", name)
		}
	}
}

func TestStaticScriptsPackage(t *testing.T) {
	paths := []string{
		"node_modules/@lsongdev/scripts/react/react.js",
		"node_modules/@lsongdev/scripts/react/preact.js",
		"node_modules/@lsongdev/scripts/react/preact-hooks.js",
		"node_modules/@lsongdev/scripts/react/htm.js",
	}
	for _, name := range paths {
		t.Run(name, func(t *testing.T) {
			data, err := files.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "from 'https://") || strings.Contains(string(data), "from\"https://") {
				t.Fatal("runtime must use package-local modules")
			}
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest("GET", "/"+name, nil))
			if w.Code != 200 || strings.Contains(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("static module response: %d %s", w.Code, w.Header().Get("Content-Type"))
			}
			if w.Body.String() != string(data) {
				t.Fatal("module was modified")
			}
			if w.Header().Get("Cache-Control") != "no-cache" {
				t.Fatal("relative dependency paths must revalidate after redeploy")
			}
		})
	}
}
