// Package outputschematest validates command output against the committed
// JSON Schema files, so each command package can prove its schema describes
// exactly what --json prints.
package outputschematest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// schemaDir walks up from the test's working directory to the module root.
func schemaDir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "docs", "site", "public", "schemas", "output")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// Validate fails t unless doc (one JSON document) satisfies the schema for the
// command at path, e.g. "ofga stores list".
func Validate(t testing.TB, path string, doc []byte) {
	t.Helper()
	file := filepath.Join(schemaDir(t), outputschema.FileName(path))
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read schema for %q (run `make schemas`): %v", path, err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	c := jsonschema.NewCompiler()
	url := outputschema.URL(path)
	if err := c.AddResource(url, schemaDoc); err != nil {
		t.Fatalf("add %s: %v", file, err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("compile %s: %v", file, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("output for %q is not JSON: %v\n%s", path, err, doc)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("output for %q violates %s:\n%v\n%s", path, outputschema.FileName(path), err, doc)
	}
}
