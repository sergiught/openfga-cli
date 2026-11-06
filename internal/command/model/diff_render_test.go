package model

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeldiff"
)

func TestRenderDiffSections(t *testing.T) {
	configtest.Isolate(t)
	out := newDiffOutput("old.fga", "new.fga", modeldiff.Result{
		Breaking: []modeldiff.Entry{{Kind: modeldiff.RelationRemoved, Type: "document", Relation: "viewer", Detail: "relation removed"}},
		Changed:  []modeldiff.Entry{{Kind: modeldiff.SchemaVersionChanged, Detail: "schema version changed"}},
		Added:    []modeldiff.Entry{{Kind: modeldiff.TypeAdded, Type: "team", Detail: "type added"}},
		Cost: []modeldiff.CostEntry{{
			Type: "document", Relation: "editor", Direction: modeldiff.CostIncreased,
			Old: map[string]int{"user": 1}, New: map[string]int{"user": -1},
		}},
	})
	var buf bytes.Buffer
	if err := renderDiff(&buf, out); err != nil {
		t.Fatal(err)
	}
	got := ansi.Strip(buf.String())
	last := -1
	for _, want := range []string{"Breaking", "document#viewer:", "Changed", "schema:", "Added", "team:", "Cost", "user 1 → inf"} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
		if i < last {
			t.Errorf("%q out of order in:\n%s", want, got)
		}
		last = i
	}
	if !strings.HasSuffix(got, "1 breaking, 1 changed, 1 added, 1 cost increase\n") {
		t.Errorf("unexpected summary line:\n%s", got)
	}
}

func TestRenderDiffNoChanges(t *testing.T) {
	configtest.Isolate(t)
	var buf bytes.Buffer
	if err := renderDiff(&buf, newDiffOutput("a", "b", modeldiff.Result{})); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ansi.Strip(buf.String()), "no changes\n") {
		t.Errorf("got:\n%s", ansi.Strip(buf.String()))
	}
}
