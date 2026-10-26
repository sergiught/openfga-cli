package tui

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func varsDoc(vs ...mapping.Variable) *mapping.Document {
	d := sampleDoc(memberAddedEvent())
	d.Rules[0].Variables = vs
	return d
}

func openVars(s *Session) component { return newVarList(s, Focus{}) }

func varNames(d *mapping.Document) string {
	var out []string
	for _, v := range d.Rules[0].Variables {
		out = append(out, v.Name)
	}
	return strings.Join(out, ",")
}

func TestVariablesAddEditDelete(t *testing.T) {
	h := newHarness(t, varsDoc(), nil, openVars)
	if !strings.Contains(plain(h.view(60, 12)), "No variables yet.") {
		t.Fatalf("the empty state is missing:\n%s", plain(h.view(60, 12)))
	}
	h.press("a")
	topAs[*varForm](t, h)
	h.typeText("org")
	h.press("tab")
	h.typeText("input.data.object.organization.id")
	h.press("esc")

	vs := h.s.Doc.Rules[0].Variables
	if len(vs) != 1 || vs[0].Name != "org" || vs[0].Expr != "input.data.object.organization.id" {
		t.Fatalf("variables = %+v", vs)
	}
	if !strings.Contains(plain(h.view(60, 12)), "org") {
		t.Fatalf("the list does not show the variable:\n%s", plain(h.view(60, 12)))
	}
	h.press("d")
	if n := len(h.s.Doc.Rules[0].Variables); n != 0 {
		t.Fatalf("variables = %d after delete", n)
	}
}

// A2: the selection stayed on the first row while the filter showed another,
// so d deleted a variable the user could not see.
func TestAFilteredVariableDeleteRemovesTheRowShown(t *testing.T) {
	h := newHarness(t, varsDoc(
		mapping.Variable{Name: "org", Expr: "input.a"},
		mapping.Variable{Name: "user", Expr: "input.b"},
		mapping.Variable{Name: "conn", Expr: "input.c"},
	), nil, openVars)
	h.press("/")
	h.typeText("conn")
	h.press("enter", "d")
	if got := varNames(h.s.Doc); got != "org,user" {
		t.Fatalf("variables = %s, want conn deleted", got)
	}
}

func TestTypingAVariableCommitsAsYouType(t *testing.T) {
	h := newHarness(t, varsDoc(mapping.Variable{}), nil, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
	h.typeText("org")
	if got := h.s.Doc.Rules[0].Variables[0].Name; got != "org" || h.changes == 0 {
		t.Fatalf("name = %q, changes = %d", got, h.changes)
	}
}

// A variable's value is an expression already, so a picked path goes in bare;
// wrapped in {{ }} it would be a string, not the value. The insert commits.
func TestAPathGoesIntoAVariableBare(t *testing.T) {
	h := newHarness(t, varsDoc(mapping.Variable{Name: "org"}), nil, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
	h.press("tab", "ctrl+p", "/")
	h.typeText("organization.id")
	h.press("enter", "enter")
	if got := h.s.Doc.Rules[0].Variables[0].Expr; got != "input.data.object.organization.id" {
		t.Fatalf("expression = %q", got)
	}
}

func TestAVariableNameIsValidatedInline(t *testing.T) {
	h := newHarness(t, varsDoc(mapping.Variable{}), nil, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
	h.typeText("1org")
	h.press("tab")
	if !strings.Contains(plain(h.view(72, 12)), "must be letters, digits and _, not starting with a digit") {
		t.Fatalf("no inline error for a bad name:\n%s", plain(h.view(72, 12)))
	}
}

func TestTabbingThroughAVariableChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		return varsDoc(mapping.Variable{Name: "org", Expr: "input.a"})
	}, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
}

// A block scalar ends in a newline and an expression can span lines; opening
// the variable and leaving must keep both.
func TestAMultilineVariableKeepsItsLines(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		return varsDoc(mapping.Variable{Name: "org", Expr: "input.a\t// first\n?? input.b\n"})
	}, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
	h := newHarness(t, varsDoc(mapping.Variable{Name: "org"}), nil, func(s *Session) component { return newVarForm(s, Focus{}, 0) })
	h.press("tab")
	h.typeText("a")
	h.press("alt+enter")
	h.typeText("b")
	if got := h.s.Doc.Rules[0].Variables[0].Expr; got != "a\nb" {
		t.Fatalf("expr = %q, want two lines", got)
	}
}
