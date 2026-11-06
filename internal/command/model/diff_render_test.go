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
	res := modeldiff.Result{
		Breaking: []modeldiff.Entry{{Kind: modeldiff.RelationRemoved, Type: "document", Relation: "viewer", Detail: "relation removed"}},
		Changed:  []modeldiff.Entry{{Kind: modeldiff.SchemaVersionChanged, Detail: "schema version changed"}},
		Added:    []modeldiff.Entry{{Kind: modeldiff.TypeAdded, Type: "team", Detail: "type added"}},
		Cost: []modeldiff.CostEntry{{
			Type: "document", Relation: "editor", Direction: modeldiff.CostIncreased,
			Old: map[string]int{"user": 1}, New: map[string]int{"user": -1},
		}},
	}
	out := newDiffOutput("old.fga", "new.fga", res)
	var buf bytes.Buffer
	if err := renderDiff(&buf, out, res); err != nil {
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
	if !strings.HasSuffix(got, "1 breaking, 1 changed, 1 added, 1 cost increase — breaking\n") {
		t.Errorf("unexpected summary line:\n%s", got)
	}
}

func TestRenderDiffNoChanges(t *testing.T) {
	configtest.Isolate(t)
	var buf bytes.Buffer
	if err := renderDiff(&buf, newDiffOutput("a", "b", modeldiff.Result{}), modeldiff.Result{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ansi.Strip(buf.String()), "no changes\n") {
		t.Errorf("got:\n%s", ansi.Strip(buf.String()))
	}
}

func TestRenderDiffVerdictOnAddedOnly(t *testing.T) {
	configtest.Isolate(t)
	res := modeldiff.Result{Added: []modeldiff.Entry{{Kind: modeldiff.TypeAdded, Type: "team", Detail: "type added"}}}
	var buf bytes.Buffer
	if err := renderDiff(&buf, newDiffOutput("a", "b", res), res); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ansi.Strip(buf.String()), "1 added — ok\n") {
		t.Errorf("got:\n%s", ansi.Strip(buf.String()))
	}
}

func TestFormatCostChangeOnlyChangedUsers(t *testing.T) {
	got := formatCostChange(modeldiff.CostEntry{
		Old: map[string]int{"user": 1, "group#member": 2, "gone": 3},
		New: map[string]int{"user": 1, "group#member": -1, "team": 1},
	})
	if want := "gone 3 → -, group#member 2 → inf, team - → 1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
