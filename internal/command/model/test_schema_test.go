package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/output"
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

// Under -o jsonl and agent-mode compaction the schema is one compact line,
// like every other command's JSON output.
func TestTestSchemaCmdIsOneLineWhenCompact(t *testing.T) {
	for name, set := range map[string]*bool{"jsonl": &output.JSONL, "compact": &output.Compact} {
		t.Run(name, func(t *testing.T) {
			defer func(v bool) { *set = v }(*set)
			*set = true
			cmd := (&Command{}).testSchemaCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			line := strings.TrimSuffix(out.String(), "\n")
			if strings.Contains(line, "\n") || !json.Valid([]byte(line)) {
				t.Fatalf("want one line of JSON, got %d lines", strings.Count(out.String(), "\n"))
			}
		})
	}
}
