package modeltest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

const manifestFileName = "ofga.yaml"

// ErrWorkspaceNotFound identifies manifest discovery failure without relying on
// error-message text. Callers can offer scaffold guidance only for this case.
var ErrWorkspaceNotFound = errors.New("model-test workspace not found")

// WorkspaceOptions supplies or overrides workspace configuration from CLI
// flags. A non-empty field replaces the corresponding manifest field. When no
// manifest is discovered, Model and Tests are required and a manifest-free
// workspace is built from these values (globs relative to path). Globs are
// relative to the discovered manifest's directory when a manifest is present.
type WorkspaceOptions struct {
	Model    string
	Fixtures []string
	Tests    []string
}

func (o WorkspaceOptions) empty() bool {
	return o.Model == "" && len(o.Fixtures) == 0 && len(o.Tests) == 0
}

// LoadWorkspace loads and validates an ofga workspace rooted at path, using
// only what's on disk. See LoadWorkspaceWith to override or supply model,
// fixtures, or tests from flags.
func LoadWorkspace(path string) (*Workspace, error) {
	return LoadWorkspaceWith(path, WorkspaceOptions{})
}

// LoadWorkspaceWith loads a workspace, applying opts. With no options it is
// identical to LoadWorkspace. With options, a discovered manifest's fields are
// overridden by any set option; if no manifest is found, the workspace is built
// entirely from the options (requiring at least Model and Tests).
func LoadWorkspaceWith(path string, opts WorkspaceOptions) (*Workspace, error) {
	if !opts.empty() {
		return loadWorkspaceWithOptions(path, opts)
	}

	info, err := os.Stat(path)
	if err != nil {
		// os.Stat's *PathError already reads "stat <path>: ..." — don't double it.
		return nil, err
	}

	var filePath, root string
	if info.IsDir() {
		found, err := findManifest(path)
		if err != nil {
			return nil, err
		}
		filePath = found
		root = filepath.Dir(found)
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", path, err)
		}
		filePath = abs
		root = filepath.Dir(abs)
	}

	if isTestFile(filePath) {
		return loadSingleTestFile(filePath, root)
	}

	ws, err := loadManifestWorkspace(filePath, root)
	if err != nil {
		return nil, err
	}

	if err := checkDuplicateTestIDs(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// loadWorkspaceWithOptions builds a workspace when flags supply or override its
// configuration. A discovered manifest is used as the base and its model /
// fixtures / tests are replaced by any set option; when no manifest is found,
// the workspace is synthesized entirely from the options.
func loadWorkspaceWithOptions(path string, opts WorkspaceOptions) (*Workspace, error) {
	root, err := resolveDir(path)
	if err != nil {
		return nil, err
	}

	var m *Manifest
	if manifestPath, ferr := findManifest(root); ferr == nil {
		m, err = parseManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		root = filepath.Dir(manifestPath)
	} else if errors.Is(ferr, ErrWorkspaceNotFound) {
		if opts.Model == "" || len(opts.Tests) == 0 {
			return nil, fmt.Errorf("no ofga.yaml found in %s or any parent directory; running from flags requires at least --model and --tests", root)
		}
		m = &Manifest{Version: 1, path: filepath.Join(root, "ofga.yaml")}
	} else {
		return nil, ferr
	}

	if opts.Model != "" {
		m.Model = opts.Model
	}
	if len(opts.Fixtures) > 0 {
		m.Fixtures = opts.Fixtures
	}
	if len(opts.Tests) > 0 {
		m.Tests = opts.Tests
	}

	ws, err := buildManifestWorkspace(m, root, root)
	if err != nil {
		return nil, err
	}
	// A `<name>.test.yaml` (or .test.yml) path argument names a single file to
	// run, even with flags set — restrict the workspace to it, mirroring
	// loadSingleTestFile on the no-options branch, so
	// `ofga model test foo.test.yaml --model x` runs only foo, not the whole
	// workspace.
	if isTestFile(path) {
		if err := restrictToTestFile(ws, path); err != nil {
			return nil, err
		}
	}
	if err := checkDuplicateTestIDs(ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// restrictToTestFile narrows ws to the single *.test.yaml file at path,
// decoding it fresh and replacing TestFiles.
func restrictToTestFile(ws *Workspace, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("read %s: %w", abs, err)
	}
	tf, err := decodeTestFile(abs, raw)
	if err != nil {
		return err
	}
	ws.TestFiles = []*TestFile{tf}
	return nil
}

// resolveDir returns the absolute directory for path: path itself if it is a
// directory, otherwise its parent.
func resolveDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return abs, nil
	}
	return filepath.Dir(abs), nil
}

// findManifest walks upward from dir looking for ofga.yaml, stopping at the
// filesystem root.
func findManifest(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}

	for {
		candidate := filepath.Join(abs, manifestFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("%w: no %s found in %s or any parent directory", ErrWorkspaceNotFound, manifestFileName, dir)
		}
		abs = parent
	}
}

// FindWorkspaceRoot discovers the nearest manifest directory without parsing
// the manifest. Watch mode uses it so a temporarily malformed ofga.yaml still
// watches the correct directory and recovers on the next save.
func FindWorkspaceRoot(path string) (string, error) {
	dir, err := resolveDir(path)
	if err != nil {
		return "", err
	}
	manifest, err := findManifest(dir)
	if err != nil {
		return "", err
	}
	return filepath.Dir(manifest), nil
}

// loadSingleTestFile loads a workspace for a single *.test.yaml file passed
// directly (e.g. via --file or a positional path). It first walks upward
// from the file's directory the same way a directory path would, looking
// for an ofga.yaml manifest: when one is found, the manifest workspace
// (model, fixtures, server config) is loaded normally but TestFiles is
// restricted to just this one file, so only its tests run — the common "run
// just this one file from my workspace" case. When no manifest is found
// walking up, the file is loaded bare (Manifest nil), for a truly
// standalone file that embeds its own `model:`.
func loadSingleTestFile(path, root string) (*Workspace, error) {
	manifestPath, err := findManifest(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			return loadBareTestFile(path, root)
		}
		return nil, err
	}

	ws, err := loadManifestWorkspace(manifestPath, filepath.Dir(manifestPath))
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tf, err := decodeTestFile(path, raw)
	if err != nil {
		return nil, err
	}

	ws.TestFiles = []*TestFile{tf}
	return ws, nil
}

func loadBareTestFile(path, root string) (*Workspace, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tf, err := decodeTestFile(path, raw)
	if err != nil {
		return nil, err
	}

	return &Workspace{Root: root, TestFiles: []*TestFile{tf}}, nil
}

func loadManifestWorkspace(path, root string) (*Workspace, error) {
	m, err := parseManifest(path)
	if err != nil {
		return nil, err
	}
	return buildManifestWorkspace(m, filepath.Dir(path), root)
}

// parseManifest reads, validates, and decodes an ofga.yaml manifest file.
func parseManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if looksLikeOfficialStoreFile(generic) {
		return nil, fmt.Errorf("%s looks like an official openfga CLI store file; ofga uses its own workspace format (see docs)", path)
	}

	data, err := yamlToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := validate(docManifest, data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	// `tuples` is an interchangeable keyword for `fixtures`; fold it in.
	m.Fixtures = append(m.Fixtures, m.Tuples...)
	m.Tuples = nil

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}
	m.path = abs
	return &m, nil
}

// buildManifestWorkspace resolves a manifest's model and expands its test and
// fixture globs (relative to manifestDir) into a Workspace.
func buildManifestWorkspace(m *Manifest, manifestDir, root string) (*Workspace, error) {
	modelAbs := filepath.Join(manifestDir, m.Model)
	if _, err := os.Stat(modelAbs); err != nil {
		return nil, fmt.Errorf("model %q not found (resolved to %s)", m.Model, modelAbs)
	}

	testFiles, err := expandTestFiles(manifestDir, m.Tests)
	if err != nil {
		return nil, err
	}

	fixtures, err := expandFixtures(manifestDir, m.Fixtures)
	if err != nil {
		return nil, err
	}

	return &Workspace{Root: root, Manifest: m, TestFiles: testFiles, Fixtures: fixtures}, nil
}

// expandFixtures registers fixture files by workspace-relative path without the
// extension. Bare basenames remain accepted by resolveFixtureRef when unique.
func expandFixtures(manifestDir string, patterns []string) (map[string]string, error) {
	reg := make(map[string]string)
	for _, pattern := range patterns {
		matches, err := doublestar.Glob(os.DirFS(manifestDir), pattern)
		if err != nil {
			return nil, fmt.Errorf("expand fixtures pattern %q: %w", pattern, err)
		}
		for _, rel := range matches {
			full := filepath.Join(manifestDir, rel)
			info, err := os.Stat(full)
			if err != nil {
				return nil, fmt.Errorf("stat fixture %s: %w", full, err)
			}
			if info.IsDir() {
				continue // a glob like fixtures/** can match directories; skip them
			}
			name := fixtureID(manifestDir, full)
			if prev, ok := reg[name]; ok && prev != full {
				return nil, fmt.Errorf("duplicate fixture id %q (%s and %s)", name, prev, full)
			}
			reg[name] = full
		}
	}
	return reg, nil
}

func fixtureID(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(rel), "fixtures/"))
	return strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
}

func checkDuplicateTestIDs(ws *Workspace) error {
	seen := make(map[string]string, len(ws.TestFiles))
	for _, tf := range ws.TestFiles {
		id := ws.TestFileID(tf)
		if prev, ok := seen[id]; ok {
			return fmt.Errorf("duplicate test-file id %q (%s and %s)", id, prev, tf.Path)
		}
		seen[id] = tf.Path
	}
	return nil
}

// looksLikeOfficialStoreFile reports whether a decoded document matches the
// shape of an official openfga CLI store file (name/model_file/tuple_file)
// rather than an ofga manifest (which always has a version key). `tuples` is
// deliberately not a discriminator: it's a legitimate ofga manifest alias for
// `fixtures`, so a versionless manifest using it should get the accurate
// "version is required" error, not a misleading store-file diagnosis.
func looksLikeOfficialStoreFile(m map[string]any) bool {
	if _, ok := m["version"]; ok {
		return false
	}

	for _, k := range []string{"name", "model_file", "tuple_file"} {
		if _, ok := m[k]; ok {
			return true
		}
	}

	return false
}

func expandTestFiles(manifestDir string, patterns []string) ([]*TestFile, error) {
	var testFiles []*TestFile
	// Overlapping patterns (e.g. "tests/**/*.test.yaml" and "tests/*.test.yaml")
	// can match the same file more than once. Dedupe by resolved path so a file
	// isn't loaded and run twice — and so it doesn't trip the duplicate-stem
	// check by "colliding" with itself.
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		matches, err := doublestar.Glob(os.DirFS(manifestDir), pattern)
		if err != nil {
			return nil, fmt.Errorf("expand tests pattern %q: %w", pattern, err)
		}

		for _, rel := range matches {
			full := filepath.Join(manifestDir, rel)
			if seen[full] {
				continue
			}
			seen[full] = true

			raw, err := os.ReadFile(full)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", full, err)
			}

			tf, err := decodeTestFile(full, raw)
			if err != nil {
				return nil, err
			}

			testFiles = append(testFiles, tf)
		}
	}

	return testFiles, nil
}

func decodeTestFile(path string, raw []byte) (*TestFile, error) {
	data, err := yamlToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := validate(docTestFile, data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var tf TestFile
	if err := yaml.Unmarshal(raw, &tf); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	setTestSourcePositions(raw, tf.Tests)
	// `tuples` is an interchangeable keyword for `fixtures` at the file level.
	tf.Fixtures = append(tf.Fixtures, tf.Tuples...)
	tf.Tuples = nil
	tf.Path = path

	seen := make(map[string]bool, len(tf.Tests))
	for _, test := range tf.Tests {
		if seen[test.Name] {
			return nil, fmt.Errorf("duplicate test name %q in %s", test.Name, path)
		}

		seen[test.Name] = true
		if len(test.Check)+len(test.ListObjects)+len(test.ListUsers) == 0 {
			return nil, fmt.Errorf("%s: test %q has no assertions; add a check, list_objects, or list_users case", path, test.Name)
		}

		// The schema requires an `assertions` key but permits an empty map; an
		// empty (or omitted) assertions block would run zero assertions and pass
		// vacuously, so reject it at load time.
		for i, cc := range test.Check {
			if len(cc.Assertions) == 0 {
				return nil, fmt.Errorf("%s: test %q check case %d has no assertions", path, test.Name, i+1)
			}
			for j, tk := range cc.ContextualTuples {
				if err := validateTuple(tk); err != nil {
					return nil, fmt.Errorf("%s: test %q check case %d contextual tuple %d: %w", path, test.Name, i+1, j+1, err)
				}
			}
		}
		for i, lc := range test.ListObjects {
			if len(lc.Assertions) == 0 {
				return nil, fmt.Errorf("%s: test %q list_objects case %d has no assertions", path, test.Name, i+1)
			}
		}
		for i, lc := range test.ListUsers {
			if len(lc.Assertions) == 0 {
				return nil, fmt.Errorf("%s: test %q list_users case %d has no assertions", path, test.Name, i+1)
			}
		}
		for _, items := range [][]TupleItem{test.Fixtures, test.Tuples} {
			for i, item := range items {
				if item.Tuple != nil {
					if err := validateTuple(*item.Tuple); err != nil {
						return nil, fmt.Errorf("%s: test %q inline tuple %d: %w", path, test.Name, i+1, err)
					}
				}
			}
		}
	}

	return &tf, nil
}

func setTestSourcePositions(raw []byte, tests []Test) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "tests" || root.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		for j, item := range root.Content[i+1].Content {
			if j >= len(tests) {
				break
			}
			tests[j].Line = item.Line
			tests[j].Column = item.Column
			if item.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(item.Content); k += 2 {
				if item.Content[k].Value == "name" {
					tests[j].Line = item.Content[k+1].Line
					tests[j].Column = item.Content[k+1].Column
					break
				}
			}
		}
		return
	}
}

// yamlToJSON parses YAML bytes and re-encodes them as JSON so they can be
// validated against a JSON Schema.
func yamlToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		// go-yaml errors already begin "yaml: ..." — the caller adds the file
		// context, so don't prepend a second "yaml:".
		return nil, err
	}

	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return out, nil
}
