package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	if err := unix.FcntlFlock(f.Fd(), unix.F_SETLKW, &lock); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		lock.Type = unix.F_UNLCK
		_ = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock)
		_ = f.Close()
	}, nil
}
