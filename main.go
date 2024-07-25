package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/song940/fileinfo-go/fileinfo"
	tmdb "github.com/song940/tmdb-go/persistent"
	"gopkg.in/yaml.v2"
)

type LibraryConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

type Library struct {
	Id int
	LibraryConfig
}

type Config struct {
	BaseDir   string          `yaml:"-"`
	Listen    string          `yaml:"listen"`
	Libraries []LibraryConfig `yaml:"libraries"`
}

type H map[string]interface{}

type File struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Size         int64  `json:"size"`
	Mode         uint32 `json:"mode"`
	IsDir        bool   `json:"isDir"`
	Path         string `json:"path"`
	Icon         string `json:"icon"`
	Hidden       bool   `json:"hidden"`
	Extension    string `json:"extension"`
	LastModified int64  `json:"lastModified"`
	FullPath     string `json:"fullPath"`
	Line1        string `json:"line1"`
	Line2        string `json:"line2"`
	Line3        string `json:"line3"`
}

type Server struct {
	Config    *Config
	Libraries []*Library
}

func LoadConfig(baseDir string) (config *Config, err error) {
	configPath := filepath.Join(baseDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	config = &Config{}
	config.BaseDir = baseDir
	err = yaml.Unmarshal(data, config)
	return
}

func NewServer(config *Config) (server *Server, err error) {
	server = &Server{
		Config: config,
	}
	return
}

// Render renders an HTML template with the provided data.
func (s *Server) Render(w http.ResponseWriter, templateName string, data H) {
	if data == nil {
		data = H{}
	}
	data["Config"] = s.Config
	tmpl, err := template.ParseFiles("templates/layout.html", "templates/"+templateName+".html")
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

// FileTypeClassifier classifies files based on their extensions.
type FileTypeClassifier struct {
	types map[string]string
}

func NewFileTypeClassifier() *FileTypeClassifier {
	return &FileTypeClassifier{
		types: map[string]string{
			".mp4":  "video",
			".mkv":  "video",
			".avi":  "video",
			".mpg":  "video",
			".mp3":  "music",
			".flac": "music",
			".jpg":  "image",
			".png":  "image",
		},
	}
}

func (c *FileTypeClassifier) Classify(extension string) string {
	if fileType, ok := c.types[extension]; ok {
		return fileType
	}
	return "file"
}

type MetaInfoHandler interface {
	Handle(f *File)
}

type MovieHandler struct {
	client *tmdb.Client
}

func (h *MovieHandler) Handle(f *File) {
	info := fileinfo.Parse(f.Name)
	res, err := h.client.SearchMovie(info.Title, nil)
	if err != nil || len(res.Results) == 0 {
		return
	}
	movie := res.Results[0]
	f.Name = movie.Title
	f.Line1 = movie.ReleaseDate
	f.Line2 = fmt.Sprintf("%1.1f/10", movie.VoteAverage)
	f.Icon = h.client.GetImage(movie.PosterPath, "")
}

type MusicHandler struct{}

func (h *MusicHandler) Handle(f *File) {
	f.Type = "music"
	f.Icon = "https://cdn-icons-png.flaticon.com/512/4039/4039628.png"
}

type ImageHandler struct{}

func (h *ImageHandler) Handle(f *File) {
	f.Type = "image"
}

func (s *Server) GetMetaInfo(f *File) {
	cfg := &tmdb.Config{}
	cfg.APIKey = "5640d0f3eea1e20a18d3a1f150b3a1ef"
	tmdbClient, err := tmdb.NewClient(cfg)
	if err != nil {
		return
	}
	handlers := map[string]MetaInfoHandler{
		"video": &MovieHandler{client: tmdbClient},
		"music": &MusicHandler{},
		"image": &ImageHandler{},
	}
	f.Type = NewFileTypeClassifier().Classify(f.Extension)
	if handler, ok := handlers[f.Type]; ok {
		handler.Handle(f)
	}
}

func (s *Server) ProcessFile(path string) (f File, err error) {
	info, err := os.Stat(path)
	f = File{
		Name:         filepath.Base(path),
		IsDir:        info.IsDir(),
		Hidden:       strings.HasPrefix(filepath.Base(path), "."),
		LastModified: info.ModTime().Unix(),
		Size:         info.Size(),
		Mode:         uint32(info.Mode().Perm()),
		Path:         path,
		FullPath:     path,
		Icon:         "https://cdn-icons-png.flaticon.com/256/607/607674.png",
	}
	f.Extension = filepath.Ext(f.Name)
	f.Line1 = fmt.Sprintf("%d bytes", f.Size)
	if f.IsDir {
		f.Icon = "https://cdn-icons-png.freepik.com/256/12532/12532956.png"
		return
	}
	s.GetMetaInfo(&f)
	return
}

func (s *Server) ListFiles(root, path string) (files []File, err error) {
	list, err := os.ReadDir(filepath.Join(root, path))
	if err != nil {
		return
	}
	for _, entry := range list {
		info, _ := entry.Info()
		f, err := s.ProcessFile(filepath.Join(root, path, info.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return
}

func (s *Server) GetPath(r *http.Request) (base, path string) {
	path = r.URL.Query().Get("path")
	source := r.URL.Query().Get("source")
	sourceIndex, _ := strconv.ParseUint(source, 10, 32)
	library := s.Config.Libraries[sourceIndex]
	return library.Path, path
}

func (s *Server) GetFile(r *http.Request) (file File, err error) {
	base, path := s.GetPath(r)
	file, err = s.ProcessFile(filepath.Join(base, path))
	return file, err
}

func (s *Server) HomeView(w http.ResponseWriter, r *http.Request) {
	s.Render(w, "home", H{})
}

func (s *Server) ListView(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	base, path := s.GetPath(r)
	files, err := s.ListFiles(base, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Render(w, "list", H{
		"source": source,
		"files":  files,
	})
}

func (s *Server) MusicView(w http.ResponseWriter, r *http.Request) {
	file, err := s.GetFile(r)
	if err != nil {
		s.Render(w, "error", H{
			"error": err.Error(),
		})
		return
	}
	s.Render(w, "music", H{
		"file": file,
	})
}

func (s *Server) MovieView(w http.ResponseWriter, r *http.Request) {
	file, err := s.GetFile(r)
	if err != nil {
		s.Render(w, "error", H{
			"error": err.Error(),
		})
		return
	}
	s.Render(w, "movie", H{
		"file": file,
	})
}

func (s *Server) FileView(w http.ResponseWriter, r *http.Request) {
	file, err := s.GetFile(r)
	if err != nil {
		s.Render(w, "error", H{
			"error": err.Error(),
		})
		return
	}
	s.Render(w, "file", H{
		"file": file,
	})
}

func (s *Server) IndexView(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	switch typ {
	case "video":
		s.MovieView(w, r)
		return
	case "music":
		s.MusicView(w, r)
		return
	case "list":
		s.ListView(w, r)
		return
	default:
		s.FileView(w, r)
		return
	}
}

func (s *Server) DownloadFile(w http.ResponseWriter, r *http.Request) {
	base, path := s.GetPath(r)
	file, err := os.Open(filepath.Join(base, path))
	if err != nil {
		http.Error(w, "File not found.", http.StatusNotFound)
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, "Unable to get file info.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	_, err = io.Copy(w, file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	baseDir, _ := os.Getwd()
	config, err := LoadConfig(baseDir)
	if err != nil {
		log.Fatal(err)
	}
	server, err := NewServer(config)
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/", server.HomeView)
	http.HandleFunc("/files", server.IndexView)
	http.HandleFunc("/download", server.DownloadFile)
	log.Fatal(http.ListenAndServe(config.Listen, nil))
}
