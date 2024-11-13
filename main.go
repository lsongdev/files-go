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

	// _ "github.com/glebarez/go-sqlite"
	_ "github.com/mattn/go-sqlite3"
)

type H = map[string]interface{}

// FileInfo 结构体新增了用于信息展示的字段
type File struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Dir   string `json:"dir"`
	Path  string `json:"path"`
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Line1 string `json:"line1"`
	Line2 string `json:"line2"`
	Line3 string `json:"line3"`
}

type FileServer struct {
	db *sql.DB

	processors []FileProcessor
}

func NewFileServer() (server *FileServer, err error) {
	db, err := sql.Open("sqlite3", "file:test.db?cache=shared&mode=memory")
	if err != nil {
		return nil, err
	}
	server = &FileServer{
		db: db,

		processors: []FileProcessor{
			&ImageProcessor{},
			// &VideoProcessor{},
			// &AudioProcessor{},
			// &TextProcessor{},
			// &PDFProcessor{},
			// &ArchiveProcessor{},
			&APKProcessor{},
			&ImageProcessor{},
			&DefaultProcessor{},
		},
	}
	server.initDB()
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
	_, err := server.db.Exec(`
		INSERT INTO files 
			(name, dir, path, is_dir, size, icon, title, line1, line2, line3) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		info.Name, info.Dir, info.Path, info.IsDir, info.Size,
		info.Icon, info.Title, info.Line1, info.Line2, info.Line3)
	return err
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

func (s *FileServer) Process(file *File) {
	p := s.GetProcessor(file)
	p.Process(file)
	s.Insert(file)
}

func (server *FileServer) ScanDirectory(root string) error {
	return filepath.Walk(root, func(filename string, f os.FileInfo, err error) error {
		if err != nil {
			log.Println("walk error", err)
			return err
		}
		dir := strings.Replace(filename, root, "", 1)
		dir = filepath.Dir(dir)
		if dir == "." || dir == "" {
			dir = "/"
		}
		// log.Println(dir, filename)
		server.Process(&File{
			Name:  f.Name(),
			Size:  f.Size(),
			IsDir: f.IsDir(),
			Path:  filename,
			Dir:   dir,
		})
		return nil
	})
}

func (server *FileServer) ListFiles(path string, offset, size int) (files []File, err error) {
	rows, err := server.db.Query(`
		SELECT name, size, path, dir, is_dir, icon, title, line1, line2, line3
		FROM files 
		WHERE dir = ? 
		LIMIT ? 
		OFFSET ?
	`, path, size, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err = rows.Scan(&file.Name, &file.Size, &file.Path, &file.Dir, &file.IsDir, &file.Icon, &file.Title, &file.Line1, &file.Line2, &file.Line3); err != nil {
			return
		}
		files = append(files, file)
	}
	return
}

func (server *FileServer) ListFilesHandler(w http.ResponseWriter, r *http.Request) (files []File) {
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

func (server *FileServer) ApiHandler(w http.ResponseWriter, r *http.Request) {
	files := server.ListFilesHandler(w, r)
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

	// root := "/Volumes/data/Videos"
	root := "/Volumes/software/Mobile/APKs"
	go server.ScanDirectory(root)

	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/file", server.FileHandler)
	http.HandleFunc("/api", server.ApiHandler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
