package files

import (
	"encoding/json"
	"fmt"
	"html/template"
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
	Config     *Config
	Libraries  []*Library
	tmdbClient *tmdb.Client
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
	cfg := &tmdb.Config{}
	cfg.APIKey = "5640d0f3eea1e20a18d3a1f150b3a1ef"
	tmdbClient, err := tmdb.NewClient(cfg)
	if err != nil {
		return
	}
	server = &Server{
		tmdbClient: tmdbClient,
		Config:     config,
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

func (s *Server) GetMetaInfo(f *File) {
	info := fileinfo.Parse(f.Name)
	switch f.Extension {
	case ".mp4", ".mkv", ".avi", ".mpg":
		isTvShow := info.Season != "" && info.Episode != ""
		if isTvShow {
			res, err := s.tmdbClient.SearchTV(info.Title, nil)
			if err != nil {
				return
			}
			if !(len(res.Results) > 0) {
				return
			}
			tvShow := res.Results[0]
			f.Type = "tvshow"
			f.Name = tvShow.Name
			f.Icon = s.tmdbClient.GetImage(tvShow.PosterPath, "")
		} else {
			res, err := s.tmdbClient.SearchMovie(info.Title, nil)
			if err != nil {
				return
			}
			if !(len(res.Results) > 0) {
				return
			}
			movie := res.Results[0]
			f.Type = "movie"
			f.Name = movie.Title
			f.Line1 = movie.ReleaseDate
			f.Line2 = fmt.Sprintf("%1.1f/10", movie.VoteAverage)
			f.Icon = s.tmdbClient.GetImage(movie.PosterPath, "")
		}
	case ".mp3":
		f.Type = "music"
		f.Icon = "https://cdn-icons-png.flaticon.com/512/4039/4039628.png"
	case ".flac":
		f.Type = "music"
		f.Icon = "https://cdn-icons-png.flaticon.com/128/14391/14391198.png"
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
			Hidden:       strings.HasPrefix(entry.Name(), "."),
			LastModified: info.ModTime().Unix(),
			Size:         info.Size(),
			Mode:         uint32(info.Mode().Perm()),
			Path:         filepath.Join(path, entry.Name()),
			FullPath:     filepath.Join(root, path, entry.Name()),
		}
		if f.IsDir {
			f.Type = "list"
			f.Icon = "https://cdn-icons-png.freepik.com/256/12532/12532956.png"
		} else {
			f.Type = "file"
			f.Extension = filepath.Ext(f.Name)
			f.Line1 = fmt.Sprintf("%d bytes", f.Size)
			f.Icon = "https://cdn-icons-png.flaticon.com/256/607/607674.png"
			s.GetMetaInfo(&f)
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

func (s *Server) HomeView(w http.ResponseWriter, r *http.Request) {
	s.Render(w, "index", H{})
}

func (s *Server) ListView(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	files, err := s.HandleListFiles(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Render(w, "list", H{
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
