//go:build !windows

package source

import (
	"os"
	"syscall"
)

// Lock grants exclusive cache ownership and is released automatically on crash.
func Lock(name string) (*os.File, error) {
	f, e := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
