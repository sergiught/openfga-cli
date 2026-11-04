package optimize

import (
	"os"
	"slices"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func TestUnexercised(t *testing.T) {
	data, err := os.ReadFile("testdata/cond_tupleset.fga")
	if err != nil {
		t.Fatal(err)
	}
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	applied := []Finding{{Type: "doc", Relation: "viewer"}, {Type: "folder", Relation: "admin"}, {Type: "doc", Relation: "viewer"}}
	got := Unexercised(m, applied, []string{"doc#parent: conditioned-only restriction folder (condition active)"})
	if !slices.Equal(got, []string{"doc#viewer"}) {
		t.Errorf("got %v", got)
	}
}

func TestUnexercisedByChecks(t *testing.T) {
	m := modelFromDSL(t, "model\n  schema 1.1\n\ntype user\n\ntype project\n  relations\n    define owner: [user]\n    define maintainer: [user] or owner\n    define contributor: [user] or maintainer or owner\n")
	applied := []Finding{{Type: "project", Relation: "contributor"}, {Type: "project", Relation: "maintainer"}}
	if got := UnexercisedByChecks(m, applied, []string{"project#owner"}); !slices.Equal(got, []string{"project#contributor", "project#maintainer"}) {
		t.Errorf("owner check: got %v", got)
	}
	if got := UnexercisedByChecks(m, applied, []string{"project#maintainer"}); !slices.Equal(got, []string{"project#contributor"}) {
		t.Errorf("a check on a dependency does not reach the relation depending on it: got %v", got)
	}
	if got := UnexercisedByChecks(m, applied, []string{"project#contributor"}); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func modelFromDSL(t *testing.T, dsl string) *Model {
	t.Helper()
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
