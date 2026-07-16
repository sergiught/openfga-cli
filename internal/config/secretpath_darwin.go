package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Default macOS filesystems are case-insensitive but case-preserving. Walk
// existing components to recover their stored spelling while still preserving
// distinct exact names on a case-sensitive volume.
func normalizeSecretPathCase(path string) string {
	volume := filepath.VolumeName(path)
	current := string(filepath.Separator)
	if volume != "" {
		current = volume + string(filepath.Separator)
	}
	rest := strings.TrimPrefix(path, current)
	parts := strings.Split(rest, string(filepath.Separator))
	for i, part := range parts {
		if part == "" {
			continue
		}
		requested := filepath.Join(current, part)
		if _, err := os.Lstat(requested); err != nil {
			// On a case-sensitive volume a differently-cased sibling is a
			// different path. Only fold case when the requested spelling
			// itself resolves, as it does on case-insensitive filesystems.
			current = requested
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return filepath.Join(current, filepath.Join(parts[i:]...))
		}
		actual := part
		for _, entry := range entries {
			if entry.Name() == part {
				actual = part
				break
			}
			if strings.EqualFold(entry.Name(), part) {
				actual = entry.Name()
			}
		}
		current = filepath.Join(current, actual)
	}
	return current
}
