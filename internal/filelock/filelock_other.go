//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"time"
)

func Lock(path string) (func(), error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) > 10*time.Minute {
			if removeErr := os.Remove(path); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock: timed out waiting for %s; remove it if no ofga process is running", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
