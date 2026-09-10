package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lsongdev/files-go/model"
)

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("local storage root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	if resolved, err := resolveExistingPrefix(abs); err == nil {
		abs = resolved
	} else {
		return nil, err
	}
	return &Local{root: abs}, nil
}

func resolveExistingPrefix(value string) (string, error) {
	candidate := value
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return value, nil
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}

func (l *Local) resolve(path string, followFinalSymlink bool) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." {
		clean = ""
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathTraversal
	}
	full := filepath.Join(l.root, clean)
	check := full
	if !followFinalSymlink {
		check = filepath.Dir(full)
	}
	resolved, err := filepath.EvalSymlinks(check)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return full, nil
		}
		return "", err
	}
	rel, err := filepath.Rel(l.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPathTraversal
	}
	return full, nil
}

func fileInfo(path string, info os.FileInfo) FileInfo {
	typeValue := model.EntryFile
	if info.IsDir() {
		typeValue = model.EntryDirectory
	} else if info.Mode()&os.ModeSymlink != 0 {
		typeValue = model.EntrySymlink
	}
	result := FileInfo{Name: info.Name(), Path: filepath.ToSlash(path), Type: typeValue, Size: info.Size(), ModifiedAt: info.ModTime()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		result.Inode = stat.Ino
		result.Device = uint64(stat.Dev)
	}
	return result
}

func (l *Local) Stat(ctx context.Context, path string) (FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return FileInfo{}, err
	}
	full, err := l.resolve(path, false)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return FileInfo{}, ErrNotFound
	}
	if err != nil {
		return FileInfo{}, err
	}
	return fileInfo(path, info), nil
}

func (l *Local) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	full, err := l.resolve(path, true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrOffline
	}
	if err != nil {
		return nil, err
	}
	result := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		child := filepath.ToSlash(filepath.Join(filepath.FromSlash(path), entry.Name()))
		result = append(result, fileInfo(child, info))
	}
	return result, nil
}

func (l *Local) Open(ctx context.Context, path string) (io.ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(path, true)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if errors.Is(err, os.ErrNotExist) {
		if _, rootErr := os.Stat(l.root); errors.Is(rootErr, os.ErrNotExist) {
			return nil, ErrOffline
		}
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Mkdir(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := l.resolve(path, false)
	if err != nil {
		return err
	}
	return os.Mkdir(full, 0755)
}

func (l *Local) Rename(ctx context.Context, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oldFull, err := l.resolve(oldPath, false)
	if err != nil {
		return err
	}
	newFull, err := l.resolve(newPath, false)
	if err != nil {
		return err
	}
	return os.Rename(oldFull, newFull)
}

func (l *Local) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := l.resolve(path, false)
	if err != nil {
		return err
	}
	if full == l.root {
		return ErrPathTraversal
	}
	return os.Remove(full)
}
