package model

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestTestSchemaCmdPrintsValidJSON verifies `model test schema` writes the
// embedded workspace schema as valid JSON to stdout.
func TestTestSchemaCmdPrintsValidJSON(t *testing.T) {
	c := &Command{}
	cmd := c.testSchemaCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("run schema cmd: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("schema output is not valid JSON: %v", err)
	}
	if _, ok := doc["$schema"]; !ok {
		t.Fatalf("schema output missing $schema key: %s", out.String())
	}
}
