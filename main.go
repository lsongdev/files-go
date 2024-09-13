package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lsongdev/apk-go/apk"
	_ "github.com/mattn/go-sqlite3"
)

type H = map[string]interface{}

type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
}

type FileServer struct {
	db   *sql.DB
	root string
}

func NewFileServer(root string) (server *FileServer, err error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	server = &FileServer{db, root}
	server.initDB()
	return
}

func (server *FileServer) initDB() error {
	_, err := server.db.Exec(`
		CREATE TABLE files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			path TEXT,
			size INTEGER,
			is_dir BOOLEAN
		)
	`)
	return err
}

func (server *FileServer) ScanDirectory() error {
	return filepath.Walk(server.root, func(filename string, info os.FileInfo, err error) error {
		if err != nil {
			log.Println(err)
			return err
		}
		// TODO: when is complete?
		dir, name := filepath.Split(filename)
		dir, _ = filepath.Rel(server.root, dir)
		if dir == ".." {
			// Ignore parent directory
			return nil
		}
		if dir == "." {
			dir = ""
		}
		dir = "/" + dir
		log.Println("Scanning", dir, name)
		_, err = server.db.Exec("INSERT INTO files (name, path, is_dir, size) VALUES (?, ?, ?, ?)", name, dir, info.IsDir(), info.Size())
		return err
	})
}

func (fs *FileServer) ListFiles(path string, offset, size int) (files []FileInfo, err error) {
	// log.Println(path, offset, size)
	rows, err := fs.db.Query("SELECT name, path, is_dir, size FROM files WHERE path = ? LIMIT ? OFFSET ?", path, size, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var file FileInfo
		if err = rows.Scan(&file.Name, &file.Path, &file.IsDir, &file.Size); err != nil {
			return
		}
		files = append(files, file)
	}
	return
}

// Render renders an HTML template with the provided data.
func (s *FileServer) Render(w http.ResponseWriter, name string, data H) {
	if data == nil {
		data = H{}
	}
	tmpl, err := template.ParseFiles("templates/layout.html", "templates/"+name+".html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = tmpl.ExecuteTemplate(w, "layout", data)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (server *FileServer) ApiHandler(w http.ResponseWriter, r *http.Request) {
	files := server.ListFilesHandler(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (server *FileServer) ListFilesHandler(w http.ResponseWriter, r *http.Request) (files []FileInfo) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if pageSize < 1 {
		pageSize = 100
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	offset := (page - 1) * pageSize
	files, err := server.ListFiles(path, offset, pageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	return
}

func (server *FileServer) IndexView(w http.ResponseWriter, r *http.Request) {
	files := server.ListFilesHandler(w, r)
	server.Render(w, "index", H{
		"files": files,
	})
}

func (server *FileServer) IconHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	filename := filepath.Join(server.root, path)
	stat, err := os.Stat(filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		http.Redirect(w, r, "https://cdn-icons-png.freepik.com/256/12532/12532956.png", http.StatusSeeOther)
		return
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png":
		http.ServeFile(w, r, filename)
	case ".apk":
		hasher := md5.New()
		io.WriteString(hasher, filename)
		cachePath := fmt.Sprintf("/tmp/%x.png", hasher.Sum(nil))
		pkg, _ := apk.Open(filename)
		defer pkg.Close()
		icon, _ := pkg.Icon(nil)
		f, _ := os.Create(cachePath)
		png.Encode(f, icon)
		http.ServeFile(w, r, cachePath)
	default:
		http.Redirect(w, r, "https://cdn-icons-png.flaticon.com/256/607/607674.png", http.StatusSeeOther)
	}
}

func (server *FileServer) FileView(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	filename := filepath.Join(server.root, path)
	http.ServeFile(w, r, filename)
}

func main() {
	root := "/Volumes/software/Mobile/APKs"
	server, err := NewFileServer(root)
	if err != nil {
		log.Fatal(err)
	}

	go server.ScanDirectory()

	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/view", server.FileView)
	http.HandleFunc("/icon", server.IconHandler)
	http.HandleFunc("/api", server.ApiHandler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
