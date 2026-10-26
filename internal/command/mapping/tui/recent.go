package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
)

// maxRecent is how many files the start screen remembers.
const maxRecent = 8

// loadRecent reads the recent-files list: absolute paths, newest first, with
// the files that no longer exist dropped. A missing or unreadable list is an
// empty one: it is a convenience, never a reason to fail. file "" keeps none.
func loadRecent(file string) []string {
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var all []string
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	var out []string
	for _, p := range all {
		if !filepath.IsAbs(p) {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		out = append(out, p)
		if len(out) == maxRecent {
			break
		}
	}
	return out
}

// rememberRecent puts path at the front of the list in file, without
// duplicates, keeping the newest maxRecent.
func rememberRecent(file, path string) error {
	if file == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	list := []string{abs}
	for _, p := range loadRecent(file) {
		if p != abs {
			list = append(list, p)
		}
	}
	list = list[:min(len(list), maxRecent)]
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	af, err := atomicfile.Create(file, 0o600)
	if err != nil {
		return err
	}
	defer af.Abort()
	if _, err := af.Write(data); err != nil {
		return err
	}
	return af.Commit()
}
