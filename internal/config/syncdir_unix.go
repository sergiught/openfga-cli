//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package config

import "os"

func syncConfigDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
