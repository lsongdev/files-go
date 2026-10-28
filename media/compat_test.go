package media

import (
	"archive/tar"
	"context"
	"encoding/binary"
	"github.com/lsongdev/files-go/store"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyExecutableDetection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.exe")
	h := make([]byte, 512)
	copy(h, "MZ")
	binary.LittleEndian.PutUint16(h[4:], 1)
	binary.LittleEndian.PutUint16(h[8:], 4)
	if err := os.WriteFile(p, h, 0600); err != nil {
		t.Fatal(err)
	}
	var m Meta
	if err := portable(p, &m); err != nil {
		t.Fatal(err)
	}
	if m.Info["Format"] != "DOS MZ" {
		t.Fatalf("wrong metadata %#v", m)
	}
	binary.LittleEndian.PutUint16(h[4:], 20)
	os.WriteFile(p, h, 0600)
	if err := portable(p, &Meta{}); err == nil {
		t.Fatal("truncated DOS file accepted")
	}
}
func TestArchiveContainerDetection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "actually-tar.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(f)
	if err = w.WriteHeader(&tar.Header{Name: "hello.txt", Size: 5, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	f.Close()
	a := Archive{Path: func(store.File) (string, error) { return p, nil }}
	var m Meta
	if err = a.Enrich(context.Background(), &store.File{}, &m); err != nil {
		t.Fatal(err)
	}
	if m.Info["Format"] != "TAR" || m.Info["Files"] != "1" {
		t.Fatalf("wrong metadata %#v", m)
	}
	os.WriteFile(p, []byte("<html>download error</html>"), 0600)
	if err = a.Enrich(context.Background(), &store.File{}, &Meta{}); err == nil {
		t.Fatal("HTML accepted as an archive")
	}
}
