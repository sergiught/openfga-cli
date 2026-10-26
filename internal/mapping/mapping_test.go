package mapping_test

import (
	"context"
	"testing"

	"github.com/openfga/mapper"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// TestMapperAPIContract pins the mapper surface this package builds on: the
// compile options, Evaluate's shape, and the trace that the preview pane reads.
func TestMapperAPIContract(t *testing.T) {
	src := []byte(`version: "1"
rules:
  - name: member-added
    when: input.type == "organization.member.added"
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: "member"
        object: "organization:{{ input.data.object.organization.id }}"
`)

	m, err := mapper.Compile(src,
		mapper.WithTrace(true),
		mapper.WithTimeout(mapping.EvalTimeout),
		mapper.WithMaxTuples(mapping.MaxTuples),
		mapper.WithMaxRules(mapping.MaxRules),
		mapper.WithMaxIteratorItems(mapping.MaxIteratorItems),
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if m.Version() != mapping.Version {
		t.Fatalf("version = %q, want %q", m.Version(), mapping.Version)
	}

	event := map[string]any{
		"type": "organization.member.added",
		"data": map[string]any{
			"object": map[string]any{
				"organization": map[string]any{"id": "org_1234"},
				"user":         map[string]any{"user_id": "auth0|507f"},
			},
		},
	}
	res, err := m.Evaluate(context.Background(), event)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(res.Tuples) != 1 {
		t.Fatalf("tuples = %d, want 1", len(res.Tuples))
	}
	got := res.Tuples[0]
	if got.User != "user:auth0|507f" || got.Relation != "member" || got.Object != "organization:org_1234" {
		t.Fatalf("tuple = %+v", got)
	}
	if res.Trace == nil || len(res.Trace.Rules) != 1 || res.Trace.Rules[0].Status != mapper.RuleMatched {
		t.Fatalf("trace = %+v", res.Trace)
	}
}

// TestDiagnosticsFromCompileError pins how compile failures reach the preview
// pane: as structured diagnostics with a field path and a 1-based position.
func TestDiagnosticsFromCompileError(t *testing.T) {
	_, err := mapper.Compile([]byte(`version: "1"
rules:
  - name: broken
    tuples:
      - user: "user:{{ input. }}"
        relation: "member"
        object: "doc:1"
`))
	if err == nil {
		t.Fatal("expected a compile error")
	}
	diags := mapper.DiagnosticsFrom(err)
	if len(diags) == 0 {
		t.Fatal("expected diagnostics")
	}
	if diags[0].Severity != mapper.SeverityError {
		t.Fatalf("severity = %q", diags[0].Severity)
	}
}
