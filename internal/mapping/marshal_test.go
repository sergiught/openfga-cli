package mapping_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got against testdata/marshal/<name>.yaml, and additionally
// compiles it: a golden that no longer compiles is a broken contract even if
// the bytes still match.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "marshal", name+".yaml")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
	if _, err := mapper.Compile(got); err != nil {
		t.Fatalf("golden %s does not compile: %v", name, err)
	}
}

func TestMarshalMinimalRule(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "organization.member.added",
		When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "member",
			Object:   "organization:{{ input.data.object.organization.id }}",
		}},
	}}}
	got, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "organization.member.added"
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: "member"
        object: "organization:{{ input.data.object.organization.id }}"
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	golden(t, "minimal", got)
}

// TestMarshalKitchenSink exercises every optional section at once and pins the
// key order the language spec documents.
func TestMarshalKitchenSink(t *testing.T) {
	d := &mapping.Document{
		Rules: []mapping.Rule{{
			Name:   "identities",
			When:   `input.type == "user.created"`,
			Action: "write",
			Variables: []mapping.Variable{
				{Name: "uid", Expr: "fga_escape(input.data.object.user_id)"},
				{Name: "tenant", Expr: "input.data.context.tenant.id"},
			},
			Iterator: &mapping.Iterator{
				Source: "input.data.object.identities",
				As:     "identity",
				Tuples: []mapping.Tuple{{
					User:     "user:{{ uid }}",
					Relation: "identity",
					Object:   "connection:{{ identity.connection }}",
					When:     "identity.isSocial",
				}},
			},
			Tuples: []mapping.Tuple{{
				User:      "user:{{ uid }}",
				Relation:  "member",
				Object:    "tenant:{{ tenant }}",
				Condition: "in_business_hours",
				Context: []mapping.ContextEntry{
					{Key: "timezone", Template: "{{ tenant }}"},
					{Key: "created_at", Template: "{{ input.data.object.created_at }}"},
				},
			}},
		}},
	}
	got, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `version: "1"
rules:
  - name: identities
    when: input.type == "user.created"
    action: write
    variables:
      uid: fga_escape(input.data.object.user_id)
      tenant: input.data.context.tenant.id
    iterator:
      source: input.data.object.identities
      as: identity
      tuples:
        - user: "user:{{ uid }}"
          relation: "identity"
          object: "connection:{{ identity.connection }}"
          when: identity.isSocial
    tuples:
      - user: "user:{{ uid }}"
        relation: "member"
        object: "tenant:{{ tenant }}"
        condition: in_business_hours
        context:
          timezone: "{{ tenant }}"
          created_at: "{{ input.data.object.created_at }}"
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	golden(t, "kitchen-sink", got)
}

func TestMarshalTupleFilters(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "sync-roles",
		When: `input.type == "organization.member.role.assigned"`,
		Filters: []mapping.TupleFilter{{
			Object: "organization:{{ input.data.object.organization.id }}",
			User:   "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Action: "patch",
		}},
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "{{ lower(input.data.object.role.name) }}",
			Object:   "organization:{{ input.data.object.organization.id }}",
		}},
	}}}
	got, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `version: "1"
rules:
  - name: sync-roles
    when: input.type == "organization.member.role.assigned"
    tuple_filters:
      - object: "organization:{{ input.data.object.organization.id }}"
        user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        action: patch
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: "{{ lower(input.data.object.role.name) }}"
        object: "organization:{{ input.data.object.organization.id }}"
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	golden(t, "tuple-filters", got)
}

// TestMarshalTestsBlock pins that embedded tests round-trip through mapper's
// own runner, not just through the emitter.
func TestMarshalTestsBlock(t *testing.T) {
	d := &mapping.Document{
		Rules: []mapping.Rule{{
			Name: "member-added",
			When: `input.type == "organization.member.added"`,
			Tuples: []mapping.Tuple{{
				User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
				Relation: "member",
				Object:   "organization:{{ input.data.object.organization.id }}",
			}},
		}},
		Tests: []mapping.TestCase{{
			Name: "member-added",
			Input: map[string]any{
				"type": "organization.member.added",
				"data": map[string]any{"object": map[string]any{
					"organization": map[string]any{"id": "org_1234"},
					"user":         map[string]any{"user_id": "auth0|507f"},
				}},
			},
			ExpectTuples: []language.Tuple{{
				User: "user:auth0|507f", Relation: "member", Object: "organization:org_1234",
			}},
		}},
	}
	got, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "with-tests", got)

	m, err := mapper.Compile(got)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, r := range m.RunTests(context.Background()) {
		if !r.Passed {
			t.Fatalf("embedded test %q failed: %v", r.Name, r.Error)
		}
	}
}

// TestMarshalIsDeterministic guards the reason Marshal builds yaml.Node trees
// by hand: Go map iteration order must not reach the output.
func TestMarshalIsDeterministic(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Variables: []mapping.Variable{
			{Name: "z", Expr: "1"}, {Name: "a", Expr: "2"}, {Name: "m", Expr: "3"},
		},
		Tuples: []mapping.Tuple{{
			User: "user:1", Relation: "r", Object: "doc:1",
			Condition: "c",
			Context: []mapping.ContextEntry{
				{Key: "z", Template: "1"}, {Key: "a", Template: "2"}, {Key: "m", Template: "3"},
			},
		}},
		Sample:   &mapping.Sample{Label: "pasted", Event: map[string]any{"type": "x"}},
		AutoName: "r",
		AutoWhen: `input.type == "x"`,
	}}}
	first, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		again, err := mapping.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, again, first)
		}
	}
	// Wizard-only bookkeeping must not leak into the file.
	for _, leak := range []string{"pasted", "AutoName", "sample"} {
		if bytesContains(first, leak) {
			t.Fatalf("%q leaked into the output:\n%s", leak, first)
		}
	}
}

func bytesContains(b []byte, s string) bool {
	return len(s) > 0 && len(b) >= len(s) && string(b) != "" && indexOf(string(b), s) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
