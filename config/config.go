package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen       string        `yaml:"listen"`
	Data         string        `yaml:"data"`
	Workers      int           `yaml:"workers"`
	ScanInterval time.Duration `yaml:"scan_interval"`
	Exclude      []string      `yaml:"exclude"`
	FFmpeg       string        `yaml:"ffmpeg"`
	FFprobe      string        `yaml:"ffprobe"`
	Auth         Auth          `yaml:"auth"`
	TMDB         TMDB          `yaml:"tmdb"`
	Roots        []Root        `yaml:"roots"`
}

type Auth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type TMDB struct {
	Key      string `yaml:"key"` // v3 API key or v4 read access token
	Language string `yaml:"language"`
}

// Root is one indexed directory. Type is a hint for media identification
// (movies, tv, music, photos, books); Types overrides it for subpaths.
type Root struct {
	ID    string            `yaml:"id"`
	Name  string            `yaml:"name"`
	Path  string            `yaml:"path"`
	Type  string            `yaml:"type"`
	Types map[string]string `yaml:"types"`
}

// TypeOf returns the media hint for a root-relative path and the base path
// the hint applies from, using the longest matching entry of Types and
// falling back to Type at the root.
func (r Root) TypeOf(path string) (hint, base string) {
	best, hint := -1, r.Type
	for prefix, value := range r.Types {
		prefix = strings.Trim(prefix, "/")
		if (path == prefix || strings.HasPrefix(path, prefix+"/")) && len(prefix) > best {
			best, hint, base = len(prefix), value, prefix
		}
	}
	return hint, base
}

var DefaultExclude = []string{".*", "node_modules", "__pycache__", "@eaDir", "#recycle", "$RECYCLE.BIN", "System Volume Information", "Thumbs.db", "desktop.ini"}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(data))), cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8088"
	}
	if cfg.Data == "" {
		cfg.Data = filepath.Dir(path)
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.ScanInterval == 0 {
		cfg.ScanInterval = 6 * time.Hour
	}
	if cfg.Exclude == nil {
		cfg.Exclude = DefaultExclude
	}
	if cfg.FFmpeg == "" {
		cfg.FFmpeg = "ffmpeg"
	}
	if cfg.FFprobe == "" {
		cfg.FFprobe = "ffprobe"
	}
	if cfg.TMDB.Language == "" {
		cfg.TMDB.Language = "zh-CN"
	}
	seen := map[string]bool{}
	for i := range cfg.Roots {
		root := &cfg.Roots[i]
		if root.Path == "" {
			return nil, fmt.Errorf("root %d: path is required", i)
		}
		abs, err := filepath.Abs(root.Path)
		if err != nil {
			return nil, err
		}
		root.Path = abs
		if root.Name == "" {
			root.Name = filepath.Base(abs)
		}
		if root.ID == "" {
			root.ID = slug(root.Name)
		}
		if seen[root.ID] {
			return nil, fmt.Errorf("duplicate root id %q", root.ID)
		}
		seen[root.ID] = true
	}
	return cfg, nil
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteByte('-')
		case r > 127:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "root"
	}
	return b.String()
}
