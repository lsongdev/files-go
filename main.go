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

var db *sql.DB

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`
		CREATE TABLE files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			path TEXT,
			is_dir BOOLEAN,
			size INTEGER
		)
	`)
	if err != nil {
		log.Fatal(err)
	}
}

func scanDirectory(root string) error {
	return filepath.Walk(root, func(filename string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		dir, name := filepath.Split(filename)
		dir, _ = filepath.Rel(root, dir)
		log.Println(dir, name)
		_, err = db.Exec("INSERT INTO files (name, path, is_dir, size) VALUES (?, ?, ?, ?)", name, dir, info.IsDir(), info.Size())
		if err != nil {
			return err
		}
		return nil
	})
}

func ListFiles(path string, offset int, size int) (files []FileInfo, err error) {
	rows, err := db.Query("SELECT name, path, is_dir, size FROM files WHERE path = ? LIMIT ? OFFSET ?", path, size, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var file FileInfo
		err = rows.Scan(&file.Name, &file.Path, &file.IsDir, &file.Size)
		if err != nil {
			return
		}
		files = append(files, file)
	}
	return
}

func IndexView(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(r.URL.Query().Get("size"))
	if err != nil || pageSize < 1 {
		pageSize = 100
	}
	path := r.URL.Query().Get("path")
	offset := (page - 1) * pageSize
	if path == "" {
		path = "."
	}
	files, err := ListFiles(path, offset, pageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func main() {
	initDB()

	root := "/Volumes/nsfw"
	go scanDirectory(root)

	http.HandleFunc("/list", IndexView)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
