//go:build linux

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func configuredDeviceIdentity(root, uuid string) (uint64, error) {
	if uuid == "" {
		return rootDeviceIdentity(root)
	}
	info, err := os.Stat(filepath.Join("/dev/disk/by-uuid", uuid))
	if err != nil {
		return 0, fmt.Errorf("resolve device_uuid %q: %w", uuid, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("resolve device_uuid %q: device identity unavailable", uuid)
	}
	return uint64(stat.Rdev), nil
}
