package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataIsTheConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("listen: \":9000\"\n")
	if cfg, err := Load(dir); err != nil || cfg.Data != dir {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
	write("data: " + dir + "/\n") // the same directory, as older configs say
	if cfg, err := Load(dir); err != nil || cfg.Data != dir {
		t.Fatalf("same data directory = %+v, %v", cfg, err)
	}
	write("data: /elsewhere\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "-d") {
		t.Fatalf("a different data directory must be rejected: %v", err)
	}
}
