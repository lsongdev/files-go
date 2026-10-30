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
	if err := portable(openFixture(t, p), &m); err != nil {
		t.Fatal(err)
	}
	if m.Info["Format"] != "DOS MZ" {
		t.Fatalf("wrong metadata %#v", m)
	}
	binary.LittleEndian.PutUint16(h[4:], 20)
	os.WriteFile(p, h, 0600)
	if err := portable(openFixture(t, p), &Meta{}); err == nil {
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
	a := Archive{Open: func(context.Context, store.File) (*os.File, error) { return os.Open(p) }}
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

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
