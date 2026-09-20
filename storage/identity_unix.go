//go:build !windows

package storage

import (
	"errors"
	"os"
	"syscall"
)

func applyFileIdentity(result *FileInfo, info os.FileInfo) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		result.Inode = uint64(stat.Ino)
		result.Device = uint64(stat.Dev)
	}
}

func rootDeviceIdentity(root string) (uint64, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev), nil
	}
	return 0, nil
}

func isDirNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
