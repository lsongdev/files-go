package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	_ "github.com/mattn/go-sqlite3"
)

type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

type FileServer struct {
	db *sql.DB
}

func NewFileServer() (server *FileServer, err error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	server = &FileServer{db: db}
	server.initDB()
	return
}

func (server *FileServer) initDB() error {
	_, err := server.db.Exec(`
		CREATE TABLE files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			path TEXT,
			is_dir BOOLEAN,
			size INTEGER
		)
	`)
	return err
}

func (fs *FileServer) ScanDirectory(root string) error {
	return filepath.Walk(root, func(filename string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		dir, name := filepath.Split(filename)
		dir, _ = filepath.Rel(root, dir)
		log.Println(dir, name)
		_, err = fs.db.Exec("INSERT INTO files (name, path, is_dir, size) VALUES (?, ?, ?, ?)", name, dir, info.IsDir(), info.Size())
		return err
	})
}

func (fs *FileServer) ListFiles(path string, offset, size int) ([]FileInfo, error) {
	rows, err := fs.db.Query("SELECT name, path, is_dir, size FROM files WHERE path = ? LIMIT ? OFFSET ?", path, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileInfo
	for rows.Next() {
		var file FileInfo
		if err := rows.Scan(&file.Name, &file.Path, &file.IsDir, &file.Size); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (fs *FileServer) IndexHandler(w http.ResponseWriter, r *http.Request) {
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
		path = "."
	}
	offset := (page - 1) * pageSize

	files, err := fs.ListFiles(path, offset, pageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func main() {
	server, err := NewFileServer()
	if err != nil {
		log.Fatal(err)
	}

	root := "/Volumes/nsfw"
	go server.ScanDirectory(root)

	http.HandleFunc("/", server.IndexHandler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
