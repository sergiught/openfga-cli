package modeltest

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// fixtureCache memoizes parsed fixture files by path for the duration of a run,
// so a file referenced by many tests is parsed once instead of once per test.
// The cached slice is treated read-only (resolveFixtures copies each tuple into
// its own result), so sharing it across the concurrent tests is safe. A nil
// *fixtureCache disables caching (used by seed and by the public wrapper).
type fixtureCache struct {
	mu     sync.Mutex
	byPath map[string][]TupleKey
	errs   map[string]error
}

func newFixtureCache() *fixtureCache {
	return &fixtureCache{byPath: map[string][]TupleKey{}, errs: map[string]error{}}
}

// load returns the parsed tuples for a fixture file, memoizing the parse (and
// any parse error) on the first request. A nil receiver parses every time.
func (c *fixtureCache) load(path string) ([]TupleKey, error) {
	if c == nil {
		return loadFixtureFile(path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.byPath[path]; ok {
		return t, nil
	}
	if err, ok := c.errs[path]; ok {
		return nil, err
	}
	t, err := loadFixtureFile(path)
	if err != nil {
		c.errs[path] = err
		return nil, err
	}
	c.byPath[path] = t
	return t, nil
}

// resolveFixtures resolves the tuple set for a single test by concatenating,
// in order: the test file's file-level fixtures (tf.Fixtures), then the test's
// own `fixtures`/`tuples` entries (interchangeable, merged) — each of which is
// either a fixture-file reference or an inline tuple. A nil cache parses every
// referenced file fresh (see fixtureCache).
//
// Tuples that collide exactly (same user, relation, object, condition name and
// context) are collapsed to one when dedupe is true; otherwise the collision is
// a hard error. Tuples that share a (user, relation, object) triple but differ
// in condition are always an error, regardless of dedupe.
func resolveFixtures(ws *Workspace, tf *TestFile, tt Test, dedupe bool, cache *fixtureCache) ([]TupleKey, error) {
	type seenTuple struct {
		fullKey string
		source  string
	}

	result := make([]TupleKey, 0)
	seen := make(map[string]seenTuple)

	addTuple := func(tk TupleKey, source string) error {
		triple := tk.User + "|" + tk.Relation + "|" + tk.Object

		condName := ""
		ctxJSON := ""
		if tk.Condition != nil {
			condName = tk.Condition.Name
			if tk.Condition.Context != nil {
				b, err := json.Marshal(tk.Condition.Context)
				if err != nil {
					return fmt.Errorf("%s: marshal condition context: %w", source, err)
				}
				ctxJSON = string(b)
			}
		}
		fullKey := triple + "|" + condName + "|" + ctxJSON

		if prev, ok := seen[triple]; ok {
			if prev.fullKey == fullKey {
				if dedupe {
					return nil
				}
				return fmt.Errorf("duplicate fixture tuple (%s, %s, %s) from %s and %s", tk.User, tk.Relation, tk.Object, prev.source, source)
			}
			return fmt.Errorf("conflicting fixture tuple (%s, %s, %s) with different condition from %s and %s", tk.User, tk.Relation, tk.Object, prev.source, source)
		}

		seen[triple] = seenTuple{fullKey: fullKey, source: source}
		result = append(result, tk)
		return nil
	}

	// addRef loads a referenced fixture file and folds in its tuples.
	addRef := func(ref string) error {
		path, err := resolveFixtureRef(ws, tf, ref)
		if err != nil {
			return err
		}
		tuples, err := cache.load(path)
		if err != nil {
			return err
		}
		for _, tk := range tuples {
			if err := addTuple(tk, path); err != nil {
				return err
			}
		}
		return nil
	}

	// File-level fixtures apply to every test in the file, first.
	for _, ref := range tf.Fixtures {
		if err := addRef(ref); err != nil {
			return nil, err
		}
	}

	// The test's own `fixtures` and `tuples` are interchangeable and merged;
	// each entry is either a reference (load the file) or an inline tuple.
	inlineSource := fmt.Sprintf("%s inline", tt.Name)
	items := append(append([]TupleItem{}, tt.Fixtures...), tt.Tuples...)
	for _, item := range items {
		if item.Tuple != nil {
			if err := addTuple(*item.Tuple, inlineSource); err != nil {
				return nil, err
			}
			continue
		}
		if err := addRef(item.Ref); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// resolveFixtureRef resolves a fixture reference to a concrete file path.
// References starting with "./" or "../" are resolved relative to the directory
// of the test file that declares them. A bare NAME resolves against the
// fixtures registered by the manifest's `fixtures` glob patterns (see
// expandFixtures) — the name is the fixture file's base without its extension.
func resolveFixtureRef(ws *Workspace, tf *TestFile, ref string) (string, error) {
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		path := filepath.Join(filepath.Dir(tf.Path), ref)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("fixture %q: %w", ref, err)
		}
		return path, nil
	}

	if path, ok := ws.Fixtures[ref]; ok {
		return path, nil
	}

	if len(ws.Fixtures) == 0 {
		return "", fmt.Errorf("fixture %q not found: no fixtures registered — add its file to the manifest `fixtures` patterns (e.g. fixtures: [\"fixtures/**/*.yaml\"]), or use a ./ or ../ path", ref)
	}
	return "", fmt.Errorf("fixture %q not registered; add its file to the manifest `fixtures` patterns (registered: %s)", ref, strings.Join(fixtureNames(ws), ", "))
}

// fixtureNames returns the registered fixture names, sorted, for error messages.
func fixtureNames(ws *Workspace) []string {
	names := make([]string, 0, len(ws.Fixtures))
	for name := range ws.Fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loadFixtureFile loads the tuples declared in a fixture file, dispatching
// on its extension.
func loadFixtureFile(path string) ([]TupleKey, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return loadYAMLTuples(path)
	case ".jsonl":
		return loadJSONLTuples(path)
	case ".csv":
		return loadCSVTuples(path)
	default:
		return nil, fmt.Errorf("fixture %s: unsupported extension", path)
	}
}

func loadYAMLTuples(path string) ([]TupleKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// TupleKey.UnmarshalYAML rejects a misspelled key (instead of silently
	// dropping it into a degenerate tuple) and accepts the compact
	// "user relation object" string form.
	dec := yaml.NewDecoder(f)
	var tuples []TupleKey
	// A fixture file may hold multiple `---`-separated YAML documents; decode
	// each and concatenate their tuples rather than stopping after the first.
	for {
		var doc []TupleKey
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		tuples = append(tuples, doc...)
	}
	for i, tk := range tuples {
		if err := validateTuple(tk); err != nil {
			return nil, fmt.Errorf("%s: tuple %d: %w", path, i+1, err)
		}
	}

	return tuples, nil
}

func loadJSONLTuples(path string) ([]TupleKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var tuples []TupleKey
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var tk TupleKey
		// DisallowUnknownFields catches a misspelled key rather than dropping it.
		jd := json.NewDecoder(strings.NewReader(line))
		jd.DisallowUnknownFields()
		if err := jd.Decode(&tk); err != nil {
			return nil, fmt.Errorf("parse %s: line %d: %w", path, lineNo, err)
		}
		if err := validateTuple(tk); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", path, lineNo, err)
		}
		tuples = append(tuples, tk)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return tuples, nil
}

// validateTuple rejects a fixture tuple missing any of the three required
// fields, so a typo'd or omitted key surfaces at load time (naming the field)
// instead of silently changing what the fixture grants or failing later with an
// opaque engine error. A tuple carrying a condition must also name it: a
// condition with a context but no name (which the JSON schema catches for
// inline test-file tuples but referenced fixture files bypass) is otherwise
// deferred to an opaque engine error.
func validateTuple(tk TupleKey) error {
	var missing []string
	if strings.TrimSpace(tk.User) == "" {
		missing = append(missing, "user")
	}
	if strings.TrimSpace(tk.Relation) == "" {
		missing = append(missing, "relation")
	}
	if strings.TrimSpace(tk.Object) == "" {
		missing = append(missing, "object")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}
	if tk.Condition != nil && strings.TrimSpace(tk.Condition.Name) == "" {
		return fmt.Errorf("condition present but its name is empty")
	}
	return nil
}

func loadCSVTuples(path string) ([]TupleKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if len(records) > 0 && len(records[0]) > 0 && records[0][0] == "user" {
		records = records[1:]
	}

	tuples := make([]TupleKey, 0, len(records))
	for i, rec := range records {
		if len(rec) < 3 {
			return nil, fmt.Errorf("%s: row %d: expected at least 3 columns (user, relation, object), got %d", path, i+1, len(rec))
		}

		tk := TupleKey{User: rec[0], Relation: rec[1], Object: rec[2]}
		if err := validateTuple(tk); err != nil {
			return nil, fmt.Errorf("%s: row %d: %w", path, i+1, err)
		}

		if len(rec) >= 4 && rec[3] != "" {
			cond := &TupleCond{Name: rec[3]}
			if len(rec) >= 5 && rec[4] != "" {
				var ctx map[string]any
				if err := json.Unmarshal([]byte(rec[4]), &ctx); err != nil {
					return nil, fmt.Errorf("%s: row %d: condition_context: %w", path, i+1, err)
				}
				cond.Context = ctx
			}
			tk.Condition = cond
		}

		tuples = append(tuples, tk)
	}

	return tuples, nil
}
