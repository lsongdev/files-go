package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigExpandsTMDBTokenAndDefaults(t *testing.T) {
	previous := ConfigDir
	ConfigDir = t.TempDir()
	t.Cleanup(func() { ConfigDir = previous })
	t.Setenv("FILES_GO_TEST_TMDB_TOKEN", "secret-token")
	data := []byte("data: " + filepath.Join(ConfigDir, "data") + "\nmedia:\n  tmdb:\n    token: ${FILES_GO_TEST_TMDB_TOKEN}\nstorages: []\nlibraries: []\n")
	if err := os.WriteFile(filepath.Join(ConfigDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Media.TMDB.Token != "secret-token" || cfg.Media.TMDB.Language != "zh-CN" || cfg.Processing.Workers != 2 {
		t.Fatalf("config = %#v", cfg)
	}
}
