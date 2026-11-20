package snapshot

import (
	"context"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func writerDoc(user, relation, object string) *mapping.Document {
	return &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: `input.type == "x"`,
		Tuples: []mapping.Tuple{{User: user, Relation: relation, Object: object}},
	}}}
}

func TestWritesAcceptsWrittenRelationAndObjectTypeWhateverTheUser(t *testing.T) {
	keep := Writes(writerDoc("user:{{ input.u }}", "member", "organization:{{ input.o }}"))
	for _, user := range []string{"user:u1", "user:*", "group:g1#member"} {
		if !keep(language.Tuple{User: user, Relation: "member", Object: "organization:o1"}) {
			t.Errorf("rejected %s member organization:o1", user)
		}
	}
	for _, tt := range []language.Tuple{
		{User: "user:u1", Relation: "admin", Object: "organization:o1"},
		{User: "user:u1", Relation: "member", Object: "team:t1"},
	} {
		if keep(tt) {
			t.Errorf("accepted %v", tt)
		}
	}
}

func TestWritesAcceptsEverythingWhenATemplateIsTemplated(t *testing.T) {
	for name, doc := range map[string]*mapping.Document{
		"relation": writerDoc("user:{{ input.u }}", "{{ input.rel }}", "organization:{{ input.o }}"),
		"type":     writerDoc("user:{{ input.u }}", "member", "{{ input.t }}:{{ input.o }}"),
	} {
		if !Writes(doc)(language.Tuple{User: "user:u1", Relation: "anything", Object: "other:o1"}) {
			t.Errorf("%s templated: rejected a tuple", name)
		}
	}
}

func TestWritesIgnoresDeleteRules(t *testing.T) {
	doc := writerDoc("user:{{ input.u }}", "member", "organization:{{ input.o }}")
	doc.Rules[0].Action = "delete"
	if Writes(doc)(language.Tuple{User: "user:u1", Relation: "member", Object: "organization:o1"}) {
		t.Error("a delete rule writes nothing")
	}
}

// Every tuple the examples' own tests expect to be written is one the
// predicate keeps, so drift never drops a desired tuple from the stored set.
func TestWritesKeepsEveryTupleTheExamplesWrite(t *testing.T) {
	for _, path := range []string{"../../../examples/mappings/auth0.yaml", "../../../examples/mappings-full/auth0.yaml"} {
		f, err := mapping.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		keep := Writes(f.Doc)
		n := 0
		for _, tc := range f.Doc.Tests {
			p := mapping.Evaluate(context.Background(), f.Doc, tc.Input)
			for _, tup := range append(append([]language.Tuple{}, p.Tuples...), tc.ExpectTuples...) {
				if tup.Action == language.ActionDelete {
					continue
				}
				n++
				if !keep(tup) {
					t.Errorf("%s test %q: rejected %s %s %s", path, tc.Name, tup.User, tup.Relation, tup.Object)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no written tuples checked", path)
		}
	}
}
