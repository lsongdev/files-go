package tmdb

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ArtifactPath(cacheDir, directory, key, extension string) (string, error) {
	key = strings.TrimSuffix(key, "."+extension)
	if len(key) != 64 {
		return "", errors.New("invalid artifact key")
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", errors.New("invalid artifact key")
	}
	if directory != "posters" || (extension != "jpg" && extension != "png") {
		return "", errors.New("invalid artifact path")
	}
	return filepath.Join(cacheDir, directory, key[:2], key[2:4], key+"."+extension), nil
}

func writeLimitedFile(filename string, source io.Reader, limit int64) (size int64, err error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".artifact-*.tmp")
	if err != nil {
		return 0, err
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	size, err = io.Copy(file, io.LimitReader(source, limit+1))
	if err != nil {
		return 0, err
	}
	if size > limit {
		return 0, errors.New("artifact exceeds size limit")
	}
	if err = file.Sync(); err != nil {
		return 0, err
	}
	if err = file.Close(); err != nil {
		return 0, err
	}
	return size, os.Rename(temporary, filename)
}
