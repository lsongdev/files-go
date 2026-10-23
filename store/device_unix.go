//go:build !windows

package store

import (
	"os"
	"syscall"
)

// deviceOf returns the filesystem device of a file, or 0 when unknown.
func deviceOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
