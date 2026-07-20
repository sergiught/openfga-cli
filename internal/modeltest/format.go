package modeltest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Workspace struct {
	Root      string
	Manifest  *Manifest
	TestFiles []*TestFile
	// Fixtures maps a registered fixture's name (its filename without extension)
	// to its absolute path, expanded from the manifest's `fixtures` glob
	// patterns. Test files reference fixtures by that name. Nil for a bare test
	// file loaded without a manifest.
	Fixtures map[string]string
}

type Manifest struct {
	Version  int            `yaml:"version"`
	Model    string         `yaml:"model"`
	Fixtures []string       `yaml:"fixtures"` // glob patterns registering fixture files, like Tests
	Tuples   []string       `yaml:"tuples"`   // interchangeable alias for Fixtures; merged in at load
	Tests    []string       `yaml:"tests"`
	Server   map[string]any `yaml:"server"`
	path     string
}

type TestFile struct {
	Path  string `yaml:"-"`
	Model string `yaml:"model"`
	// Fixtures / Tuples are interchangeable keywords for the file-level list of
	// fixtures every test in this file uses; Tuples is merged into Fixtures at
	// load. (This top-level Tuples is fixture references — distinct from a
	// test's own inline Test.Tuples.)
	Fixtures []string `yaml:"fixtures"`
	Tuples   []string `yaml:"tuples"`
	Tests    []Test   `yaml:"tests"`
}

type Test struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Fixtures and Tuples are interchangeable keywords for this test's setup
	// tuples. Each entry is either a fixture reference (a string) or an inline
	// tuple (a mapping); the two lists are merged at resolution.
	Fixtures    []TupleItem       `yaml:"fixtures"`
	Tuples      []TupleItem       `yaml:"tuples"`
	Check       []CheckCase       `yaml:"check"`
	ListObjects []ListObjectsCase `yaml:"list_objects"`
	ListUsers   []ListUsersCase   `yaml:"list_users"`
}

// TupleItem is one entry in a test's `fixtures`/`tuples` list: either a fixture
// reference (Ref — a registered fixture name, or a ./ or ../ path) or an inline
// tuple (Tuple). Exactly one is set, decided by whether the YAML entry is a
// scalar or a mapping.
type TupleItem struct {
	Ref   string
	Tuple *TupleKey
}

func (it *TupleItem) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		// A single token (no whitespace) is a fixture reference — a registered
		// name, or a ./ or ../ path. A scalar with two or more whitespace-
		// separated tokens is an attempted compact tuple ("user relation
		// object"); route it through parseCompactTuple so a malformed one (not
		// exactly three fields) errors clearly rather than being mistaken for a
		// fixture reference.
		if len(strings.Fields(value.Value)) >= 2 {
			tk, err := parseCompactTuple(value.Value)
			if err != nil {
				return err
			}
			it.Tuple = &tk
			return nil
		}
		it.Ref = value.Value
		return nil
	}
	var tk TupleKey
	if err := value.Decode(&tk); err != nil {
		return err
	}
	it.Tuple = &tk
	return nil
}

type TupleKey struct {
	User      string     `yaml:"user"`
	Relation  string     `yaml:"relation"`
	Object    string     `yaml:"object"`
	Condition *TupleCond `yaml:"condition"`
}

// UnmarshalYAML lets a tuple be written either as a mapping
// ({user, relation, object, condition}) or as the compact string form
// "user relation object" (the same order as `ofga tuples write`). A conditioned
// tuple must use the mapping form. Unknown mapping keys are rejected.
func (tk *TupleKey) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		parsed, err := parseCompactTuple(value.Value)
		if err != nil {
			return err
		}
		*tk = parsed
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("tuple must be a mapping or a \"user relation object\" string")
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		switch key := value.Content[i].Value; key {
		case "user", "relation", "object", "condition":
		default:
			return fmt.Errorf("unknown field %q in tuple", key)
		}
	}
	// Decode into a twin type (no custom unmarshaler) to avoid recursion.
	type rawTupleKey struct {
		User      string     `yaml:"user"`
		Relation  string     `yaml:"relation"`
		Object    string     `yaml:"object"`
		Condition *TupleCond `yaml:"condition"`
	}
	var raw rawTupleKey
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*tk = TupleKey(raw)
	return nil
}

// parseCompactTuple parses the "user relation object" shorthand into a TupleKey.
func parseCompactTuple(s string) (TupleKey, error) {
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return TupleKey{}, fmt.Errorf("tuple %q: want three whitespace-separated fields \"user relation object\" (use the mapping form for a condition)", s)
	}
	return TupleKey{User: fields[0], Relation: fields[1], Object: fields[2]}, nil
}

type TupleCond struct {
	Name    string         `yaml:"name"`
	Context map[string]any `yaml:"context"`
}

type CheckCase struct {
	User             string          `yaml:"user"`
	Users            []string        `yaml:"users"` // group form: share Assertions across many users
	Object           string          `yaml:"object"`
	Objects          []string        `yaml:"objects"` // group form: share Assertions across many objects
	Context          map[string]any  `yaml:"context"`
	ContextualTuples []TupleKey      `yaml:"contextual_tuples"`
	Assertions       map[string]bool `yaml:"assertions"`
}

// subjects returns the effective users and objects a check case asserts over:
// its singular field, or its plural group field. Setting both the singular and
// plural of either is a usage error.
func (cc CheckCase) subjects() (users, objects []string, err error) {
	switch {
	case cc.User != "" && len(cc.Users) > 0:
		return nil, nil, fmt.Errorf("check case sets both `user` and `users`; use one")
	case len(cc.Users) > 0:
		users = dedupeStrings(cc.Users)
	default:
		users = []string{cc.User}
	}
	switch {
	case cc.Object != "" && len(cc.Objects) > 0:
		return nil, nil, fmt.Errorf("check case sets both `object` and `objects`; use one")
	case len(cc.Objects) > 0:
		objects = dedupeStrings(cc.Objects)
	default:
		objects = []string{cc.Object}
	}
	return users, objects, nil
}

// dedupeStrings returns s with duplicate values removed, preserving first-seen
// order, so a grouped check with a repeated user/object doesn't emit duplicate
// Check calls and result rows.
func dedupeStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

type ListObjectsCase struct {
	User       string              `yaml:"user"`
	Type       string              `yaml:"type"`
	Context    map[string]any      `yaml:"context"`
	Assertions map[string][]string `yaml:"assertions"`
}

type ListUsersCase struct {
	Object     string                          `yaml:"object"`
	UserFilter []ListUsersFilter               `yaml:"user_filter"`
	Context    map[string]any                  `yaml:"context"`
	Assertions map[string]ListUsersExpectation `yaml:"assertions"`
}

type ListUsersFilter struct {
	Type     string `yaml:"type"`
	Relation string `yaml:"relation"`
}

type ListUsersExpectation struct {
	Users []string `yaml:"users"`
}

// UnmarshalYAML accepts a list_users expectation as either the flat form
// (`relation: [user:anne, user:bob]`, parallel to list_objects) or the wrapped
// form (`relation: {users: [...]}`, matching the official OpenFGA CLI).
func (e *ListUsersExpectation) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		return value.Decode(&e.Users)
	}
	type rawLUExpect struct {
		Users []string `yaml:"users"`
	}
	var raw rawLUExpect
	if err := value.Decode(&raw); err != nil {
		return err
	}
	e.Users = raw.Users
	return nil
}
