package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var ConfigDir = filepath.Join(os.Getenv("HOME"), ".filesgo")

type Config struct {
	Listen    string    `yaml:"listen"`
	Data      string    `yaml:"data"`
	CacheDir  string    `yaml:"-"`
	Database  string    `yaml:"database,omitempty"`
	Storages  []Storage `yaml:"storages"`
	Libraries []Library `yaml:"libraries"`

	TMDB struct {
		APIKey string `yaml:"api_key"`
	} `yaml:"tmdb"`
}

type Library struct {
	ID      string          `yaml:"id"`
	Name    string          `yaml:"name"`
	Type    string          `yaml:"type"`
	Sources []LibrarySource `yaml:"sources"`

	// Deprecated v0 fields are accepted and normalized at load time.
	Path     string `yaml:"path,omitempty"`
	Slug     string `yaml:"slug,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type Storage struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Path       string `yaml:"path"`
	DeviceUUID string `yaml:"device_uuid,omitempty"`
}

type LibrarySource struct {
	Storage string `yaml:"storage"`
	Path    string `yaml:"path"`
}

func (c *Config) FindLibraryIndex(name string) int {
	for index, library := range c.Libraries {
		if library.Name == name {
			return index
		}
	}
	return -1
}

func (c *Config) FindLibraryBySlug(slug string) int {
	for index, library := range c.Libraries {
		if library.Slug == slug || library.ID == slug {
			return index
		}
	}
	return -1
}

func (c *Config) GetLibrarySlug(index int) string {
	if index >= 0 && index < len(c.Libraries) {
		if c.Libraries[index].Slug != "" {
			return c.Libraries[index].Slug
		}
		return c.Libraries[index].ID
	}
	return ""
}

func LoadConfig() (cfg *Config, err error) {
	f, err := os.Open(filepath.Join(ConfigDir, "config.yaml"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8088"
	}
	if cfg.Data == "" {
		if cfg.Database != "" {
			cfg.Data = cfg.Database
		} else {
			cfg.Data = ConfigDir
		}
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(cfg.Data, "cache")
	}
	if cfg.Database == "" {
		cfg.Database = cfg.Data
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return
}

func (c *Config) normalize() error {
	storageIDs := make(map[string]bool, len(c.Storages))
	for index := range c.Storages {
		item := &c.Storages[index]
		if item.Type == "" {
			item.Type = "local"
		}
		if item.Name == "" {
			item.Name = item.ID
		}
		if item.ID == "" || item.Path == "" {
			return fmt.Errorf("storage id and path are required")
		}
		if item.Type != "local" {
			return fmt.Errorf("storage %q: unsupported type %q", item.ID, item.Type)
		}
		if storageIDs[item.ID] {
			return fmt.Errorf("duplicate storage id %q", item.ID)
		}
		storageIDs[item.ID] = true
	}
	for index := range c.Libraries {
		library := &c.Libraries[index]
		if library.ID == "" {
			library.ID = library.Slug
		}
		if library.ID == "" {
			library.ID = slugify(library.Name)
		}
		if library.Type == "" {
			library.Type = "files"
		}
		if library.Path != "" && len(library.Sources) == 0 {
			storageID := "library-" + library.ID
			if !storageIDs[storageID] {
				c.Storages = append(c.Storages, Storage{ID: storageID, Name: library.Name, Type: "local", Path: library.Path})
				storageIDs[storageID] = true
			}
			library.Sources = []LibrarySource{{Storage: storageID, Path: ""}}
		}
		for sourceIndex := range library.Sources {
			source := &library.Sources[sourceIndex]
			source.Path = strings.Trim(filepath.ToSlash(filepath.Clean(source.Path)), "/")
			if source.Path == "." {
				source.Path = ""
			}
			if source.Path == ".." || strings.HasPrefix(source.Path, "../") {
				return fmt.Errorf("library %q source path escapes storage root", library.ID)
			}
			if !storageIDs[source.Storage] {
				return fmt.Errorf("library %q references unknown storage %q", library.ID, source.Storage)
			}
		}
	}
	return nil
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return -1
	}, value)
	if value == "" {
		return "library"
	}
	return value
}
