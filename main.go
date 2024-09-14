package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	_ "github.com/glebarez/go-sqlite"

	"github.com/lsongdev/apk-go/apk"
)

type H = map[string]interface{}

type FileServer struct {
	db         *sql.DB
	root       string
	cache      []FileInfo
	processors []FileProcessor
}

func NewFileServer(root string) (server *FileServer, err error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	server = &FileServer{
		db:   db,
		root: root,
	}
	server.processors = []FileProcessor{
		&ImageProcessor{},
		&APKProcessor{
			cache: map[string]FileInfo{},
		},
		&DefaultProcessor{},
	}
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

// 在 ScanDirectory 方法中使用处理器
func (server *FileServer) ScanDirectory() error {
	return filepath.Walk(server.root, func(filename string, f os.FileInfo, err error) error {
		if err != nil {
			log.Println("walk error", err)
			return err
		}
		dir, name := filepath.Split(filename)
		dir, _ = filepath.Rel(server.root, dir)
		if dir == ".." {
			return nil
		}
		if dir == "." {
			dir = ""
		}
		dir = "/" + dir
		log.Println(filename)
		info := FileInfo{
			Name:  name,
			Path:  dir,
			Size:  f.Size(),
			IsDir: f.IsDir(),
		}
		server.cache = append(server.cache, info)
		// _, err = server.db.Exec("INSERT INTO files (name, path, is_dir, size) VALUES (?, ?, ?, ?)", name, dir, f.IsDir(), f.Size())
		// if err != nil {
		// 	log.Println("sql error:", err)
		// }
		processor := server.GetProcessor(filename)
		processor.Process(filename)
		return nil
	})
}

// FileProcessor 接口定义了文件处理器应该实现的方法
type FileProcessor interface {
	IsSupport(filename string) bool
	Process(filename string) error
	GetInfo(filename string, info *FileInfo)
}

// FileInfo 结构体新增了用于信息展示的字段
type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Title string
	Icon  string `json:"icon"`
	Line1 string `json:"line1"`
	Line2 string `json:"line2"`
	Line3 string `json:"line3"`
}

// ImageProcessor 实现了 FileProcessor 接口，用于处理图片文件
type ImageProcessor struct{}

func (p *ImageProcessor) IsSupport(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}

func (p *ImageProcessor) Process(filename string) error {
	// 对于图片，我们可以直接使用原文件作为图标，所以这里不需要额外处理
	return nil
}

func (p *ImageProcessor) GetInfo(filename string, info *FileInfo) {
	info.Icon = fmt.Sprintf("/file?path=%s", filename)
}

// APKProcessor 实现了 FileProcessor 接口，用于处理 APK 文件
type APKProcessor struct {
	cache map[string]FileInfo
}

func (p *APKProcessor) IsSupport(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".apk"
}

func (p *APKProcessor) Process(filename string) error {
	info := FileInfo{}
	pkg, err := apk.Open(filename)
	if err != nil {
		return err
	}
	defer pkg.Close()

	icon, err := pkg.Icon(nil)
	if err != nil {
		return err
	}
	hasher := md5.New()
	io.WriteString(hasher, filename)
	tmpfile := fmt.Sprintf("/tmp/%x.png", hasher.Sum(nil))

	f, err := os.Create(tmpfile)
	if err != nil {
		return err
	}
	defer f.Close()
	info.Icon = fmt.Sprintf("/file?path=%s", tmpfile)
	info.Title, _ = pkg.Label(nil)
	info.Line1 = pkg.PackageName()
	p.cache[filename] = info
	return png.Encode(f, icon)
}

func (p *APKProcessor) GetInfo(filename string, info *FileInfo) {
	in := p.cache[filename]
	info.Icon = in.Icon
	info.Title = in.Title
	info.Line1 = in.Line1
}

// DefaultProcessor 实现了 FileProcessor 接口，用于处理其他类型的文件
type DefaultProcessor struct{}

func (p *DefaultProcessor) IsSupport(filename string) bool {
	return true // 默认处理器支持所有文件
}

func (p *DefaultProcessor) Process(filename string) error {
	// 对于默认处理器，不需要特殊处理
	return nil
}

func (p *DefaultProcessor) GetInfo(filename string, info *FileInfo) {
	if info.IsDir {
		info.Icon = "https://cdn-icons-png.freepik.com/256/12532/12532956.png"
	} else {
		info.Icon = "https://cdn-icons-png.flaticon.com/256/607/607674.png" // 默认图标
	}
}

func (server *FileServer) GetProcessor(filename string) FileProcessor {
	for _, processor := range server.processors {
		if processor.IsSupport(filename) {
			return processor
		}
	}
	return nil
}

func (server *FileServer) ListFiles(path string, offset, size int) (files []FileInfo, err error) {
	for _, file := range server.cache {
		if file.Path == path {
			filename := filepath.Join(server.root, file.Path, file.Name)
			processor := server.GetProcessor(filename)
			processor.GetInfo(filename, &file)
			files = append(files, file)
		}
	}
	// rows, err := server.db.Query("SELECT name, size, path, is_dir FROM files WHERE path = ? LIMIT ? OFFSET ?", path, size, offset)
	// if err != nil {
	// 	return
	// }
	// defer rows.Close()
	// for rows.Next() {
	// 	var file FileInfo
	// 	if err = rows.Scan(&file.Name, &file.Size, &file.Path, &file.IsDir); err != nil {
	// 		return
	// 	}
	// 	filename := filepath.Join(server.root, file.Path, file.Name)
	// 	processor := server.GetProcessor(filename)
	// 	processor.GetInfo(filename, &file)
	// 	files = append(files, file)
	// }
	return
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
	root := "/Volumes/home/Videos"
	server, err := NewFileServer(root)
	if err != nil {
		log.Fatal(err)
	}

	go server.ScanDirectory()

	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/file", server.FileHandler)
	http.HandleFunc("/api", server.ApiHandler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
