package server

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/emersion/go-webdav"
)

func (fs *FileServer) localPath(path string) (int, string, error) {
	parts := strings.Split(path, "/")
	// log.Println(source)
	index := fs.config.FindLibraryIndex(parts[2])
	library := fs.config.Libraries[index]
	path = strings.Replace(path, fmt.Sprintf("/-/%s", library.Name), "", 1)
	path = filepath.Join(library.Path, path)
	// log.Println("loadPath", path)
	return index, path, nil
}

func (fs *FileServer) externalPath(source int, path string) (string, error) {
	library := fs.config.Libraries[source]
	path = strings.Replace(path, fmt.Sprintf("/-/%s", library.Name), "", 1)
	// log.Println("externalPath", path)
	rel, err := filepath.Rel(library.Path, path)
	if err != nil {
		return "", err
	}
	return "/" + filepath.ToSlash(rel), nil
}

func (fs *FileServer) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	_, p, err := fs.localPath(name)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func fileInfoFromOS(p string, fi os.FileInfo) *webdav.FileInfo {
	return &webdav.FileInfo{
		Path:    p,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
		// TODO: fallback to http.DetectContentType?
		MIMEType: mime.TypeByExtension(path.Ext(p)),
		// RFC 2616 section 13.3.3 describes strong ETags. Ideally these would
		// be checksums or sequence numbers, however these are expensive to
		// compute. The modification time with nanosecond granularity is good
		// enough, as it's very unlikely for the same file to be modified twice
		// during a single nanosecond.
		ETag: fmt.Sprintf("%x%x", fi.ModTime().UnixNano(), fi.Size()),
	}
}

func (fs *FileServer) Stat(ctx context.Context, name string) (*webdav.FileInfo, error) {
	_, p, err := fs.localPath(name)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return fileInfoFromOS(name, fi), nil
}

func (fs *FileServer) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	index, path, err := fs.localPath(name)
	if err != nil {
		return nil, err
	}

	var l []webdav.FileInfo
	err = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		href, err := fs.externalPath(index, p)
		if err != nil {
			return err
		}

		l = append(l, *fileInfoFromOS(href, fi))

		if !recursive && fi.IsDir() && path != p {
			return filepath.SkipDir
		}
		return nil
	})
	return l, err
}

func (fs *FileServer) Create(ctx context.Context, name string) (io.WriteCloser, error) {
	_, p, err := fs.localPath(name)
	if err != nil {
		return nil, err
	}
	return os.Create(p)
}

func (fs *FileServer) RemoveAll(ctx context.Context, name string) error {
	_, p, err := fs.localPath(name)
	if err != nil {
		return err
	}

	// WebDAV semantics are that it should return a "404 Not Found" error in
	// case the resource doesn't exist. We need to Stat before RemoveAll.
	if _, err = os.Stat(p); err != nil {
		return err
	}

	return os.RemoveAll(p)
}

func (fs *FileServer) Mkdir(ctx context.Context, name string) error {
	_, p, err := fs.localPath(name)
	if err != nil {
		return err
	}
	return os.Mkdir(p, 0755)
}

func copyRegularFile(src, dst string, perm os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		// TODO: send http.StatusConflict on os.IsNotExist
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Close()
}

func (fs *FileServer) Copy(ctx context.Context, src, dst string, options *webdav.CopyOptions) (created bool, err error) {
	_, srcPath, err := fs.localPath(src)
	if err != nil {
		return false, err
	}
	_, dstPath, err := fs.localPath(dst)
	if err != nil {
		return false, err
	}

	// TODO: "Note that an infinite-depth COPY of /A/ into /A/B/ could lead to
	// infinite recursion if not handled correctly"

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return false, err
	}
	srcPerm := srcInfo.Mode() & os.ModePerm

	if _, err := os.Stat(dstPath); err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		created = true
	} else {
		if options.NoOverwrite {
			return false, os.ErrExist
		}
		if err := os.RemoveAll(dstPath); err != nil {
			return false, err
		}
	}

	err = filepath.Walk(srcPath, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if fi.IsDir() {
			if err := os.Mkdir(dstPath, srcPerm); err != nil {
				return err
			}
		} else {
			if err := copyRegularFile(srcPath, dstPath, srcPerm); err != nil {
				return err
			}
		}

		if fi.IsDir() && options.NoRecursive {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return false, err
	}

	return created, nil
}

func (fs *FileServer) Move(ctx context.Context, src, dst string, options *webdav.MoveOptions) (created bool, err error) {
	_, srcPath, err := fs.localPath(src)
	if err != nil {
		return false, err
	}
	_, dstPath, err := fs.localPath(dst)
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(dstPath); err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		created = true
	} else {
		if options.NoOverwrite {
			return false, os.ErrExist
		}
		if err := os.RemoveAll(dstPath); err != nil {
			return false, err
		}
	}

	if err := os.Rename(srcPath, dstPath); err != nil {
		return false, err
	}

	return created, nil
}
