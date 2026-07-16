// Package readlimit provides bounded reads for user-controlled files, stdin,
// and HTTP bodies.
package readlimit

import (
	"fmt"
	"io"
	"os"
)

const (
	Config      int64 = 4 << 20
	Secret      int64 = 1 << 20
	Document    int64 = 64 << 20
	APIResponse int64 = 64 << 20
)

// All reads at most max bytes and returns an actionable error if more data is
// available. label identifies the input in the error message.
func All(r io.Reader, max int64, label string) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: max + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s exceeds the %d MiB limit", label, max>>20)
	}
	return data, nil
}

// File opens path and reads it through All.
func File(path string, max int64, label string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer func() { _ = file.Close() }()
	return All(file, max, label)
}

// SecretPermissionWarning reports a regular secret file that is readable or
// writable by group/other users. It is advisory because mounted container
// secrets do not always permit chmod.
func SecretPermissionWarning(path, label string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("warning: %s %s is accessible by other users; restrict it with chmod 600", label, path)
}
