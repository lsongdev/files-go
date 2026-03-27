package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var ConfigDir = filepath.Join(os.Getenv("HOME"), ".filesgo")

type Config struct {
	Listen    string `yaml:"listen"`
	CacheDir  string
	Database  string `yaml:"database"`
	Libraries []Library `yaml:"libraries"`

	TMDB struct {
		APIKey string `yaml:"api_key"`
	} `yaml:"tmdb"`
}

type Library struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Slug     string `yaml:"slug"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
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
		if library.Slug == slug {
			return index
		}
	}
	return -1
}

func (c *Config) GetLibrarySlug(index int) string {
	if index >= 0 && index < len(c.Libraries) {
		return c.Libraries[index].Slug
	}
	return ""
}

func LoadConfig() (cfg *Config, err error) {
	f, err := os.Open(filepath.Join(ConfigDir, "config.yaml"))
	if err != nil {
		return nil, err
	}
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(ConfigDir, "cache")
	}
	if cfg.Database == "" {
		cfg.Database = filepath.Join(ConfigDir, "database")
	}
	return
}
