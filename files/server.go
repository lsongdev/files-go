package files

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type LibraryConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	Path string `yaml:"path"`
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
	Hidden       bool   `json:"hidden"`
	Extension    string `json:"extension"`
	LastModified int64  `json:"lastModified"`
	Thumbnail    string `json:"thumbnail"`
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

type Library struct {
	Id int
	LibraryConfig
}

func NewServer(config *Config) *Server {
	return &Server{
		Config: config,
	}
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

func (s *Server) ListFiles(root, path string) (files []File, err error) {
	if path == "" {
		path = "."
	}
	list, err := os.ReadDir(filepath.Join(root, path))
	if err != nil {
		return
	}
	for _, entry := range list {
		info, _ := entry.Info()
		f := File{
			Name:         entry.Name(),
			IsDir:        entry.IsDir(),
			Path:         filepath.Join(path, entry.Name()),
			Hidden:       strings.HasPrefix(entry.Name(), "."),
			LastModified: info.ModTime().Unix(),
			Size:         info.Size(),
			Mode:         uint32(info.Mode().Perm()),
		}
		if f.IsDir {
			f.Type = "dir"
		} else {
			f.Type = "file"
			f.Extension = filepath.Ext(f.Name)
		}
		files = append(files, f)
	}
	return
}

func (s *Server) HandleListFiles(r *http.Request) (files []File, err error) {
	source := r.URL.Query().Get("source")
	path := r.URL.Query().Get("path")
	hidden := r.URL.Query().Has("hidden") && r.URL.Query().Get("hidden") != "false"
	sourceIndex, _ := strconv.ParseUint(source, 10, 32)
	library := s.Config.Libraries[sourceIndex]
	files, err = s.ListFiles(library.Path, path)
	if err != nil {
		return
	}
	if !hidden {
		for i := 0; i < len(files); i++ {
			if files[i].Hidden {
				files = append(files[:i], files[i+1:]...)
				i--
			}
		}
	}
	return
}

func (s *Server) ListView(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	files, err := s.HandleListFiles(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Render(w, "index", H{
		"source": source,
		"files":  files,
	})
}

func (s *Server) ListAPI(w http.ResponseWriter, r *http.Request) {
	files, err := s.HandleListFiles(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(files)
}
