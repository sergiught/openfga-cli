//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// Package filelock holds an exclusive cross-process lock on a file, released
// when the returned func is called (or the process exits).
package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Lock blocks until it holds the exclusive lock on path, creating it if needed.
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
