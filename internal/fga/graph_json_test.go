package fga

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
)

// OUT-12: `model graph --json` must emit snake_case field names and serialize
// empty slices as [] rather than null.
func TestGraphJSONTags(t *testing.T) {
	g := ParseModel(githubModel())
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, key := range []string{`"schema_version"`, `"types"`, `"edges"`, `"relations"`, `"name"`} {
		if !strings.Contains(out, key) {
			t.Errorf("graph JSON missing snake_case key %s: %s", key, out)
		}
	}
	for _, bad := range []string{`"SchemaVersion"`, `"Relations"`} {
		if strings.Contains(out, bad) {
			t.Errorf("graph JSON leaked Go-cased key %s", bad)
		}
	}
}

func TestGraphJSONEmptySlices(t *testing.T) {
	// A model with a type that has no relations must serialize its relations
	// (and the graph's edges) as [] not null.
	m := &openfga.AuthorizationModel{
		SchemaVersion:   "1.1",
		TypeDefinitions: []openfga.TypeDefinition{{Type: "user"}},
	}
	b, err := json.Marshal(ParseModel(m))
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, "null") {
		t.Errorf("empty collections should serialize as [], got null: %s", out)
	}
	if !strings.Contains(out, `"edges":[]`) || !strings.Contains(out, `"relations":[]`) {
		t.Errorf("expected empty [] slices, got: %s", out)
	}
}

// The weighted-graph heatmap exposes each relation's cost in JSON.
func TestGraphJSONWeightFields(t *testing.T) {
	b, err := json.Marshal(ParseModel(githubModel()))
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, key := range []string{`"weight"`, `"recursive"`} {
		if !strings.Contains(out, key) {
			t.Errorf("graph JSON missing %s: %s", key, out)
		}
	}
}
