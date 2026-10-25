package tui

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// foundDepth is how many directories below dir findMappings looks into.
	foundDepth = 2
	// maxFound caps the start screen's list, so a directory of thousands of
	// YAML files does not become thousands of rows.
	maxFound = 50
	// maxMappingBytes is how much of a file mappingRules reads. A mapping is a
	// few KiB of YAML; the cap keeps a huge file from stalling the start
	// screen, which reads every candidate before it draws. Pipes and devices
	// never get this far: they are refused before they are opened.
	maxMappingBytes = 1 << 20
)

// foundFile is a mapping file the start screen found by looking, and how many
// rules it holds.
type foundFile struct {
	path  string
	rules int
}

// findMappings lists mapping files under dir, looking at dir and foundDepth
// levels of folders below it and skipping .git, node_modules and vendor. Paths
// are relative to dir and sorted. A file counts only if mappingRules accepts it,
// so the other YAML in a repository (CI config, compose files) stays out.
func findMappings(dir string) []foundFile {
	var out []foundFile
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			if len(out) >= maxFound {
				return
			}
			name := e.Name()
			switch {
			case e.IsDir():
				if depth < foundDepth && !skippedDir(name) {
					walk(filepath.Join(rel, name), depth+1)
				}
			case isYAML(name):
				p := filepath.Join(rel, name)
				if n, ok := mappingRules(filepath.Join(dir, p)); ok {
					out = append(out, foundFile{path: p, rules: n})
				}
			}
		}
	}
	walk("", 0)
	slices.SortFunc(out, func(a, b foundFile) int { return strings.Compare(a.path, b.path) })
	return out
}

func skippedDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == "vendor"
}

func isYAML(name string) bool { return hasExt(name, []string{".yaml", ".yml"}) }

// mappingRules reports whether path parses as YAML with a version key and a
// rules sequence, and how many rules. It reads untrusted files, so it opens
// only regular files, reads at most maxMappingBytes, and decodes into yaml
// nodes rather than values: aliases are not expanded, so a hostile document
// cannot grow past the bytes it was read from.
func mappingRules(path string) (int, bool) {
	// Checked before opening: open(2) on a named pipe blocks until a writer
	// shows up, which would freeze the program before it draws.
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxMappingBytes+1))
	if err != nil || len(data) > maxMappingBytes {
		return 0, false
	}
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil || root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return 0, false
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return 0, false
	}
	var hasVersion bool
	var rules *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "version":
			hasVersion = true
		case "rules":
			rules = top.Content[i+1]
		}
	}
	if !hasVersion || rules == nil || rules.Kind != yaml.SequenceNode {
		return 0, false
	}
	return len(rules.Content), true
}
