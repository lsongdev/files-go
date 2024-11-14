package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	_ "github.com/glebarez/go-sqlite"
	// _ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

type H = map[string]interface{}

type Library struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}

type Config struct {
	Libraries []Library `json:"libraries"`
}

type File struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Path  string `json:"path"`
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Line1 string `json:"line1"`
	Line2 string `json:"line2"`
	Line3 string `json:"line3"`
}

func (f *File) filename() string {
	return filepath.Join(f.Path, f.Name)
}

type FileServer struct {
	config *Config
	db     *sql.DB

	processors []FileProcessor
}

func NewFileServer() (server *FileServer, err error) {
	var config *Config
	f, err := os.Open("config.yaml")
	if err != nil {
		return nil, err
	}
	if err := yaml.NewDecoder(f).Decode(&config); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:test.db?cache=shared&mode=memory") //
	if err != nil {
		return nil, err
	}
	server = &FileServer{
		config: config,
		db:     db,
	}
	server.initDB()
	server.initProcessors()
	return
}

func (server *FileServer) initDB() error {
	_, err := server.db.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			path TEXT,
			size INTEGER,
			dir TEXT,
			is_dir BOOLEAN,
			icon TEXT,
			title TEXT,
			line1 TEXT,
			line2 TEXT,
			line3 TEXT,
			UNIQUE(name, path)
		);
	`)
	return err
}

func (server *FileServer) Insert(info *File) error {
	log.Println(info.Path)
	_, err := server.db.Exec(`
		INSERT INTO files
			(name, path, is_dir, size, icon, title, line1, line2, line3) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		info.Name, info.Path, info.IsDir, info.Size,
		info.Icon, info.Title, info.Line1, info.Line2, info.Line3)
	return err
}

// Render renders an HTML template with the provided data.
func (s *FileServer) Render(w http.ResponseWriter, name string, data H) {
	if data == nil {
		data = H{}
	}
	data["Config"] = s.config
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

func (s *FileServer) Process(file *File) {
	p := s.GetProcessor(file)
	if err := p.Process(file); err != nil {
		log.Println(err)
	}
	if err := s.Insert(file); err != nil {
		log.Fatal(err)
	}
}

func (server *FileServer) ScanDirectory(root string) error {
	return filepath.Walk(root, func(filename string, f os.FileInfo, err error) error {
		if err != nil {
			log.Fatal("walk error", err)
			return err
		}
		server.Process(&File{
			Name:  f.Name(),
			Size:  f.Size(),
			IsDir: f.IsDir(),
			Path:  filepath.Dir(filename),
		})
		return nil
	})
}

func (server *FileServer) ScanLibraries() {
	for _, library := range server.config.Libraries {
		if err := server.ScanDirectory(library.Path); err != nil {
			log.Println(err)
		}
	}
}

func (server *FileServer) ListFiles(path string, offset, size int) (files []File, err error) {
	rows, err := server.db.Query(`
		SELECT name, size, path, is_dir, icon, title, line1, line2, line3
		FROM files 
		WHERE path = ? 
		LIMIT ? 
		OFFSET ?
	`, path, size, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err = rows.Scan(&file.Name, &file.Size, &file.Path, &file.IsDir, &file.Icon, &file.Title, &file.Line1, &file.Line2, &file.Line3); err != nil {
			return
		}
		log.Println(file)
		files = append(files, file)
	}
	return
}

func (server *FileServer) ListFilesHandler(w http.ResponseWriter, r *http.Request) (index int, files []File) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if pageSize < 1 {
		pageSize = 100
	}
	path := r.URL.Query().Get("path")
	source := r.URL.Query().Get("source")
	index, _ = strconv.Atoi(source)
	prefix := server.config.Libraries[index].Path
	fullpath := filepath.Join(prefix, path)
	offset := (page - 1) * pageSize
	files, err := server.ListFiles(fullpath, offset, pageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i, file := range files {
		file.Path = strings.Replace(file.Path, prefix, "", 1)
		files[i] = file
	}
	return
}

func (server *FileServer) IndexView(w http.ResponseWriter, r *http.Request) {
	server.Render(w, "index", H{})
}

func (server *FileServer) ListView(w http.ResponseWriter, r *http.Request) {
	source, files := server.ListFilesHandler(w, r)
	server.Render(w, "list", H{
		"source": source,
		"path":   r.URL.Query().Get("path"),
		"files":  files,
	})
}

func (server *FileServer) ApiHandler(w http.ResponseWriter, r *http.Request) {
	_, files := server.ListFilesHandler(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (server *FileServer) FileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	http.ServeFile(w, r, path)
}

func main() {
	server, err := NewFileServer()
	if err != nil {
		log.Fatal(err)
	}

	go server.ScanLibraries()

	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/files", server.ListView)
	http.HandleFunc("/file", server.FileHandler)
	http.HandleFunc("/api", server.ApiHandler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
