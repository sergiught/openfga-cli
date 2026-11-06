package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Workspace is one converted sample store, ready to be written as an ofga
// model-test workspace.
type Workspace struct {
	Manifest, Model, Fixture, Tests []byte
}

// The upstream types below declare exactly the keys we map. They are decoded
// with KnownFields(true), so any upstream key not listed here fails the
// conversion loudly instead of being dropped.

type upstreamStore struct {
	Name      string         `yaml:"name"` // informational; the sandbox names stores itself
	Model     string         `yaml:"model"`
	ModelFile string         `yaml:"model_file"`
	Tuples    []tuple        `yaml:"tuples"`
	TupleFile string         `yaml:"tuple_file"`
	Tests     []upstreamTest `yaml:"tests"`
}

type upstreamTest struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Tuples      []tuple           `yaml:"tuples"`
	TupleFile   string            `yaml:"tuple_file"`
	Check       []checkCase       `yaml:"check"`
	ListObjects []listObjectsCase `yaml:"list_objects"`
	ListUsers   []listUsersCase   `yaml:"list_users"`
}

type tuple struct {
	User      string     `yaml:"user"`
	Relation  string     `yaml:"relation"`
	Object    string     `yaml:"object"`
	Condition *condition `yaml:"condition,omitempty"`
}

type condition struct {
	Name    string   `yaml:"name"`
	Context *rawNode `yaml:"context,omitempty"`
}

type checkCase struct {
	User             string          `yaml:"user"`
	Object           string          `yaml:"object"`
	Context          *rawNode        `yaml:"context,omitempty"`
	ContextualTuples []tuple         `yaml:"contextual_tuples,omitempty"`
	Assertions       map[string]bool `yaml:"assertions"`
}

type listObjectsCase struct {
	User       string              `yaml:"user"`
	Type       string              `yaml:"type"`
	Context    *rawNode            `yaml:"context,omitempty"`
	Assertions map[string][]string `yaml:"assertions"`
}

type listUsersCase struct {
	Object     string                   `yaml:"object"`
	UserFilter []userFilter             `yaml:"user_filter"`
	Context    *rawNode                 `yaml:"context,omitempty"`
	Assertions map[string]usersExpected `yaml:"assertions"`
}

type userFilter struct {
	Type     string `yaml:"type"`
	Relation string `yaml:"relation,omitempty"`
}

type usersExpected struct {
	Users []string `yaml:"users"`
}

// Output (ofga) shapes. They reuse the upstream case types, which share field
// names and order with the ofga test schema; only the envelope differs.

type outManifest struct {
	Version  int      `yaml:"version"`
	Model    string   `yaml:"model"`
	Fixtures []string `yaml:"fixtures"`
	Tests    []string `yaml:"tests"`
}

type outTestFile struct {
	Fixtures []string  `yaml:"fixtures"`
	Tests    []outTest `yaml:"tests"`
}

type outTest struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Tuples      []tuple           `yaml:"tuples,omitempty"`
	Check       []checkCase       `yaml:"check,omitempty"`
	ListObjects []listObjectsCase `yaml:"list_objects,omitempty"`
	ListUsers   []listUsersCase   `yaml:"list_users,omitempty"`
}

// Convert is ConvertNamed with a generic fixture name.
func Convert(storeFile []byte, readFile func(rel string) ([]byte, error)) (Workspace, error) {
	return ConvertNamed("store", storeFile, readFile)
}

// ConvertNamed converts an upstream store.fga.yaml into an ofga model-test
// workspace whose single fixture is called name. readFile resolves paths
// (model_file, tuple_file) relative to the store file's directory.
func ConvertNamed(name string, storeFile []byte, readFile func(rel string) ([]byte, error)) (Workspace, error) {
	var src upstreamStore
	if err := decodeStrict(storeFile, &src); err != nil {
		return Workspace{}, fmt.Errorf("parse store file: %w", err)
	}

	model, err := resolveModel(src, readFile)
	if err != nil {
		return Workspace{}, err
	}

	fixture := append([]tuple{}, src.Tuples...)
	if src.TupleFile != "" {
		extra, err := readTupleFile(src.TupleFile, readFile)
		if err != nil {
			return Workspace{}, err
		}
		fixture = append(fixture, extra...)
	}

	tf := outTestFile{Fixtures: []string{name}}
	for _, t := range src.Tests {
		ot := outTest{
			Name:        t.Name,
			Description: t.Description,
			Tuples:      t.Tuples,
			Check:       t.Check,
			ListObjects: t.ListObjects,
			ListUsers:   t.ListUsers,
		}
		if t.TupleFile != "" {
			extra, err := readTupleFile(t.TupleFile, readFile)
			if err != nil {
				return Workspace{}, fmt.Errorf("test %q: %w", t.Name, err)
			}
			ot.Tuples = append(append([]tuple{}, ot.Tuples...), extra...)
		}
		tf.Tests = append(tf.Tests, ot)
	}

	manifest := outManifest{
		Version:  1,
		Model:    "./model.fga",
		Fixtures: []string{"fixtures/*.yaml"},
		Tests:    []string{"tests/*.test.yaml"},
	}

	var ws Workspace
	ws.Model = []byte(model)
	if ws.Manifest, err = marshal(manifest); err != nil {
		return Workspace{}, err
	}
	if ws.Fixture, err = marshal(fixture); err != nil {
		return Workspace{}, err
	}
	if ws.Tests, err = marshal(tf); err != nil {
		return Workspace{}, err
	}
	return ws, nil
}

func resolveModel(src upstreamStore, readFile func(string) ([]byte, error)) (string, error) {
	switch {
	case src.Model != "" && src.ModelFile != "":
		return "", errors.New("store sets both model and model_file")
	case src.Model != "":
		return src.Model, nil
	case src.ModelFile != "":
		b, err := readFile(src.ModelFile)
		if err != nil {
			return "", fmt.Errorf("read model_file: %w", err)
		}
		return string(b), nil
	}
	return "", errors.New("store sets neither model nor model_file")
}

func readTupleFile(rel string, readFile func(string) ([]byte, error)) ([]tuple, error) {
	b, err := readFile(rel)
	if err != nil {
		return nil, fmt.Errorf("read tuple_file: %w", err)
	}
	var tuples []tuple
	if err := decodeStrict(b, &tuples); err != nil {
		return nil, fmt.Errorf("parse tuple_file %s: %w", rel, err)
	}
	return tuples, nil
}

func decodeStrict(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// rawNode keeps a context mapping exactly as written (scalar styles and
// quoting included) and is exempt from the strict key check, since context
// keys are user data rather than schema.
type rawNode struct{ node yaml.Node }

func (r *rawNode) UnmarshalYAML(n *yaml.Node) error {
	r.node = *n
	return nil
}

func (r rawNode) MarshalYAML() (any, error) { return &r.node, nil }
