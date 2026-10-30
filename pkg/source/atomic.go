package source

import (
	"io"
	"os"
	"path/filepath"
)

// Atomic writes a complete cache entry through a unique sibling temp file.
func Atomic(name string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
func WriteFile(name string, data []byte) error {
	return Atomic(name, func(w io.Writer) error { _, e := w.Write(data); return e })
}

// Writer stops streamed output at a fixed byte budget without buffering it.
type Writer struct {
	W         io.Writer
	Remaining int64
	Exceeded  bool
}

func (w *Writer) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		w.Exceeded = true
		return 0, ErrLimit
	}
	n, e := w.W.Write(p)
	w.Remaining -= int64(n)
	return n, e
}
