//go:build windows

package storage

import (
	"errors"
	"os"
	"syscall"
)

func applyFileIdentity(_ *FileInfo, _ os.FileInfo) {}

func rootDeviceIdentity(root string) (uint64, error) {
	_, err := os.Stat(root)
	return 0, err
}

func isDirNotEmpty(err error) bool {
	return errors.Is(err, syscall.ERROR_DIR_NOT_EMPTY) || errors.Is(err, os.ErrExist)
}
