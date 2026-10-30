package source

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRootAndDescriptorResistReplacement(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(out, "secret"), []byte("secret"), 0600)
	os.Mkdir(filepath.Join(root, "dir"), 0700)
	p := filepath.Join(root, "dir", "file")
	os.WriteFile(p, []byte("inside"), 0600)
	r, e := OpenRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	f, e := r.Open("dir/file")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	os.Remove(p)
	if e := os.Symlink(filepath.Join(out, "secret"), p); e != nil {
		t.Skip(e)
	}
	if _, e := r.Open("dir/file"); e == nil {
		t.Fatal("escaped through file symlink")
	}
	os.RemoveAll(filepath.Join(root, "dir"))
	os.Symlink(out, filepath.Join(root, "dir"))
	if _, e := r.Open("dir/secret"); e == nil {
		t.Fatal("escaped through directory symlink")
	}
	if _, e := r.Open("../secret"); e == nil {
		t.Fatal("accepted traversal")
	}
	b, e := io.ReadAll(f)
	if e != nil || string(b) != "inside" {
		t.Fatalf("pinned file: %q %v", b, e)
	}
	if runtime.GOOS != "windows" {
		f.Seek(0, 0)
		b, e = Output(Command(context.Background(), "cat", f, Input), 1024)
		if e != nil || string(b) != "inside" {
			t.Fatalf("child reopened source: %q %v", b, e)
		}
	}
	link := filepath.Join(t.TempDir(), "root")
	os.Symlink(root, link)
	if r, e := OpenRoot(link); e == nil {
		r.Close()
		t.Fatal("accepted symlink root")
	}
}

func TestConcurrentSymlinkReplacementCannotEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("SECRET"), 0600)
	target := filepath.Join(root, "file")
	os.WriteFile(target, []byte("inside"), 0600)
	r, e := OpenRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	link := filepath.Join(root, "link")
	if e := os.Symlink(secret, link); e != nil {
		t.Skip(e)
	}
	os.Remove(link)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			os.Symlink(secret, link)
			os.Rename(link, target)
			tmp := filepath.Join(root, "good")
			os.WriteFile(tmp, []byte("inside"), 0600)
			os.Rename(tmp, target)
		}
	}()
	for i := 0; i < 400; i++ {
		f, e := r.Open("file")
		if e != nil {
			continue
		}
		b, e := io.ReadAll(f)
		f.Close()
		if e == nil && string(b) != "inside" {
			t.Errorf("escaped: %q", b)
			break
		}
	}
	<-done
}
