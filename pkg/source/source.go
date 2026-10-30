// Package source provides rooted read-only files and descriptor-based subprocess input.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrChanged = errors.New("source changed")
var ErrLimit = errors.New("resource limit exceeded")

// Root pins a configured directory for the duration of an operation. Its parent
// configuration path is trusted; replacing the root itself with a symlink is rejected.
type Root struct{ root *os.Root }

func OpenRoot(name string) (*Root, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("root is not a directory: %s", name)
	}
	r, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := r.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		r.Close()
		return nil, ErrChanged
	}
	return &Root{r}, nil
}
func (r *Root) Close() error                       { return r.root.Close() }
func (r *Root) Stat() (os.FileInfo, error)         { return r.root.Stat(".") }
func (r *Root) Open(name string) (*os.File, error) { return r.open(name, false) }
func (r *Root) Dir(name string) (*os.File, error)  { return r.open(name, true) }
func (r *Root) open(name string, dir bool) (*os.File, error) {
	if name == "" {
		name = "."
	}
	if !filepath.IsLocal(name) || strings.ContainsRune(name, 0) {
		return nil, errors.New("invalid source path")
	}
	f, err := r.root.OpenFile(name, readFlags(), 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if dir && !info.IsDir() || !dir && !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("source has unexpected file type")
	}
	return f, nil
}

// File supplies a fresh handle per operation. Key is a stable caller-defined
// identity; Open must enforce the caller's access boundary on every use.
type File struct {
	Key  string
	Open func(context.Context) (*os.File, error) `json:"-"`
}

func Local(name string) File {
	abs, _ := filepath.Abs(name)
	return File{Key: abs, Open: func(ctx context.Context) (*os.File, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := OpenRoot(filepath.Dir(abs))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return r.Open(filepath.Base(abs))
	}}
}
func (f File) Stat(ctx context.Context) (os.FileInfo, error) {
	h, e := f.Open(ctx)
	if e != nil {
		return nil, e
	}
	defer h.Close()
	return h.Stat()
}

// Input is replaced by a seekable inherited descriptor. The child never
// reopens the original media path. Descriptor inheritance requires Unix.
const Input = "{source-input}"

func Command(ctx context.Context, bin string, f *os.File, args ...string) *exec.Cmd {
	f.Seek(0, 0)
	args = append([]string(nil), args...)
	for i, arg := range args {
		if arg == Input {
			args[i] = "/dev/fd/3"
		}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.ExtraFiles = []*os.File{f}
	return cmd
}

// Buffer bounds captured output; overflow stops the command's output copier.
type Buffer struct {
	Data     []byte
	Limit    int
	Exceeded bool
}

func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.Limit - len(b.Data)
	if len(p) > left {
		b.Data = append(b.Data, p[:max(0, left)]...)
		b.Exceeded = true
		return max(0, left), ErrLimit
	}
	b.Data = append(b.Data, p...)
	return n, nil
}
func (b *Buffer) String() string { return string(b.Data) }
func Output(cmd *exec.Cmd, limit int) ([]byte, error) {
	out, stderr := &Buffer{Limit: limit}, &Buffer{Limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	if out.Exceeded {
		return nil, ErrLimit
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", cmd.Path, err, stderr.String())
	}
	return out.Data, nil
}
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, ErrLimit
	}
	return b, e
}
