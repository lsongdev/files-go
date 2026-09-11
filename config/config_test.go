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

func TestLoadConfigReadsTMDBTokenFileWhenEnvironmentIsEmpty(t *testing.T) {
	configDir := t.TempDir()
	previous := ConfigDir
	ConfigDir = configDir
	t.Cleanup(func() { ConfigDir = previous })
	t.Setenv("FILES_GO_EMPTY_TMDB_TOKEN", "")
	if err := os.WriteFile(filepath.Join(configDir, ".tmdb-token"), []byte("local-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data := []byte("data: " + configDir + "\nmedia:\n  tmdb:\n    token: ${FILES_GO_EMPTY_TMDB_TOKEN}\n    token_file: .tmdb-token\nstorages: []\nlibraries: []\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Media.TMDB.Token != "local-secret" {
		t.Fatalf("TMDB token = %q", cfg.Media.TMDB.Token)
	}
}
