package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), "../secret.txt"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("Open traversal error = %v, want ErrPathTraversal", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), "escape/secret.txt"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("Open symlink escape error = %v, want ErrPathTraversal", err)
	}
}

func TestLocalOpenIsSeekable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "video.bin"), []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := local.Open(context.Background(), "video.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(5, 0); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := file.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "56" {
		t.Fatalf("read = %q, want 56", buf)
	}
}
