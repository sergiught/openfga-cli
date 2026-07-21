package modeltest

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/workspace.v1.json
var schemaBytes []byte

const schemaResourceName = "workspace.v1.json"

// WorkspaceSchemaURL is the canonical web URL editors can use for completion
// and validation without generating a local copy.
const WorkspaceSchemaURL = "https://raw.githubusercontent.com/sergiught/openfga-cli/main/internal/modeltest/schema/workspace.v1.json"

// WorkspaceSchema returns the embedded workspace JSON schema bytes, so a command
// can print it (e.g. to seed an editor's `$schema` binding). The returned slice
// backs the embedded schema and must not be mutated.
func WorkspaceSchema() []byte {
	return schemaBytes
}

// docKind identifies which part of the workspace schema a document should be
// validated against.
type docKind int

const (
	docManifest docKind = iota
	docTestFile
)

var (
	compileOnce    sync.Once
	compileErr     error
	manifestSchema *jsonschema.Schema
	testFileSchema *jsonschema.Schema
)

// validate checks data (already-decoded JSON bytes) against the embedded
// workspace schema for the given kind.
func validate(kind docKind, data []byte) error {
	sch, err := schemaFor(kind)
	if err != nil {
		return err
	}

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("schema: decode instance: %w", err)
	}

	if err := sch.Validate(inst); err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	return nil
}

// ValidateTestFile validates raw YAML test-file bytes against the test file
// schema. It is the same validation the CLI performs when loading a test
// file, exposed for callers (such as the playground editor) that only have
// unparsed bytes.
func ValidateTestFile(data []byte) error {
	json, err := yamlToJSON(data)
	if err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	return validate(docTestFile, json)
}

func schemaFor(kind docKind) (*jsonschema.Schema, error) {
	compileOnce.Do(compileSchemas)
	if compileErr != nil {
		return nil, compileErr
	}

	switch kind {
	case docManifest:
		return manifestSchema, nil
	case docTestFile:
		return testFileSchema, nil
	default:
		return nil, fmt.Errorf("schema: unknown kind %d", kind)
	}
}

func compileSchemas() {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		compileErr = fmt.Errorf("schema: decode embedded schema: %w", err)
		return
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResourceName, doc); err != nil {
		compileErr = fmt.Errorf("schema: add resource: %w", err)
		return
	}

	manifestSchema, err = compiler.Compile(schemaResourceName + "#/$defs/manifest")
	if err != nil {
		compileErr = fmt.Errorf("schema: compile manifest: %w", err)
		return
	}

	testFileSchema, err = compiler.Compile(schemaResourceName + "#/$defs/testFile")
	if err != nil {
		compileErr = fmt.Errorf("schema: compile testFile: %w", err)
		return
	}
}
