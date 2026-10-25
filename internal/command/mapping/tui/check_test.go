package tui

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openCheck(fs []mapping.Finding, model, modelPath string, missing []string) func(*Session) component {
	return func(s *Session) component {
		c := newCheckView(s)
		c.set(fs, model, modelPath, missing)
		return c
	}
}

func TestCheckRowsJumpToTheRule(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{Name: "first"}, {}}}
	fs := []mapping.Finding{
		{Message: "no file", Rule: -1},
		{Message: "bad relation", Rule: 1, Section: "tuple"},
		{Message: "bad when", Rule: 0, Section: "trigger"},
	}
	h := newHarness(t, d, nil, openCheck(fs, "model.fga", "model.fga", nil))
	v := plain(h.view(80, 14))
	for _, want := range []string{"document — no file", "rule 2 › tuple — bad relation", "first › trigger — bad when"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
	if _, sub := h.top().(headed).heading(); sub != "model.fga · m change" {
		t.Fatalf("subtitle = %q", sub)
	}
	h.press("enter") // the document row does nothing
	if h.closed {
		t.Fatal("a document row closed the pane")
	}
	h.press("down", "enter")
	if !h.closed || h.result != (Jump{Rule: 1, Section: "tuple"}) {
		t.Fatalf("closed = %v, result = %v", h.closed, h.result)
	}

	h = newHarness(t, d, nil, openCheck(fs, "", "", nil))
	h.press("m")
	if !h.closed || h.result != RequestModel {
		t.Fatalf("m: closed = %v, result = %v", h.closed, h.result)
	}
}

func TestCheckEmptyWithAndWithoutAModel(t *testing.T) {
	d := &mapping.Document{}
	h := newHarness(t, d, nil, openCheck(nil, "model.fga", "", nil))
	v := plain(h.view(80, 10))
	for _, want := range []string{"✓ No problems.", "mapper accepts the mapping", "every type and relation it names is in model.fga"} {
		if !strings.Contains(v, want) {
			t.Fatalf("with a model lacks %q:\n%s", want, v)
		}
	}
	h = newHarness(t, d, nil, openCheck(nil, "", "", nil))
	if v := plain(h.view(80, 10)); !strings.Contains(v, "– No model: only mapper's checks ran (m to choose one).") {
		t.Fatalf("without a model:\n%s", v)
	}
}

func TestCheckNamesTheTypesTheModelLacks(t *testing.T) {
	d := &mapping.Document{}
	h := newHarness(t, d, nil, openCheck(nil, "m", "model.fga", []string{"a", "b"}))
	if v := plain(h.view(120, 10)); !strings.Contains(v, "The model lacks: a, b. Add them to model.fga, then push it with 'ofga model write'.") {
		t.Fatalf("with a path:\n%s", v)
	}
	h = newHarness(t, d, nil, openCheck(nil, "m", "", []string{"a", "b"}))
	v := plain(h.view(120, 10))
	if !strings.Contains(v, "The model lacks: a, b.") || strings.Contains(v, "Add ") {
		t.Fatalf("without a path:\n%s", v)
	}
	// The block never pushes the view past its height, and a short pane
	// trims the type list, not the instruction.
	many := make([]string, 30)
	for i := range many {
		many[i] = "type" + string(rune('a'+i%26))
	}
	fs := make([]mapping.Finding, 20)
	for i := range fs {
		fs[i] = mapping.Finding{Message: "x", Rule: -1}
	}
	h = newHarness(t, d, nil, openCheck(fs, "m", "model.fga", many))
	out := h.view(40, 8)
	if n := len(strings.Split(out, "\n")); n > 8 {
		t.Fatalf("%d lines, want at most 8", n)
	}
	flat := strings.Join(strings.Fields(plain(out)), " ")
	if !strings.Contains(flat, "ofga model write") || !strings.Contains(flat, "more)") {
		t.Fatalf("instruction lost or list not trimmed:\n%s", out)
	}
}

func TestCheckBadge(t *testing.T) {
	if got := checkBadge(make([]mapping.Finding, 3), true); got != "✗ 3" {
		t.Fatalf("findings: %q", got)
	}
	if got := checkBadge(nil, true); got != "✓" {
		t.Fatalf("clean: %q", got)
	}
	if got := checkBadge(nil, false); got != "–" {
		t.Fatalf("no model: %q", got)
	}
}

func TestCheckRowsCannotDriveTheTerminal(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{Name: "\x1b[31mevil\x1b]0;x\x07"}}}
	fs := []mapping.Finding{{Message: "bad\x1b[2J", Rule: 0, Section: "action"}}
	h := newHarness(t, d, nil, openCheck(fs, "mo\x1b[2Jdel", "pa\x1b[2Jth", []string{"ty\x1b[2Jpe"}))
	if v := h.view(120, 10); strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b]") || strings.Contains(v, "\x07") {
		t.Fatalf("an escape reached the screen: %q", v)
	}
}

// The workspace feeds Check again after every pause in typing, so a user
// moving through its rows while editing elsewhere stays on the row they chose.
func TestCheckKeepsItsSelectionAcrossSet(t *testing.T) {
	fs := []mapping.Finding{
		{Message: "first", Rule: -1}, {Message: "second", Rule: -1}, {Message: "third", Rule: -1},
	}
	h := newHarness(t, &mapping.Document{}, nil, openCheck(fs, "", "", nil))
	h.view(80, 14)
	h.press("down", "down")
	c := topAs[*checkView](t, h)
	c.set(fs, "", "", nil)
	h.view(80, 14)
	if it, _ := c.list.Selected(); it.Index != 2 {
		t.Fatalf("after set the selection is row %d, want 2", it.Index)
	}
	c.set(fs[:2], "", "", nil)
	if it, _ := c.list.Selected(); it.Index != 1 {
		t.Fatalf("after fewer rows the selection is row %d, want the last, 1", it.Index)
	}
}

func TestCheckRowsNameTheFieldInTheRule(t *testing.T) {
	h := loadedWs(t, "version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuples:\n      - relation: member\n        object: team:1\n")
	var row string
	for _, f := range h.w.findings {
		if f.Rule == 0 && strings.Contains(f.Field, "tuples[0].user") {
			row = f.Field
		}
	}
	if row == "" {
		t.Fatalf("no finding for the missing user: %+v", h.w.findings)
	}
	h.press("2")
	if v := flat(h.view()); !strings.Contains(v, flat("r › tuples[0].user — is required")) {
		t.Fatalf("the row lacks its field:\n%s", h.view())
	}
}

func TestCheckRowsFallBackToTheSectionAndShowADocumentField(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{Name: "a"}}}
	fs := []mapping.Finding{
		{Message: "no sample", Rule: 0, Section: "trigger"},
		{Message: "bad", Rule: -1, Field: "version"},
	}
	h := newHarness(t, d, nil, openCheck(fs, "", "", nil))
	v := plain(h.view(80, 10))
	for _, want := range []string{"a › trigger — no sample", "version — bad"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestANarrowCheckRowKeepsItsMessage(t *testing.T) {
	name := strings.Repeat("r", 30)
	field := "rules[0].tuple_filters[0].object_type_is_a_long_field"
	msg := "this message is long enough to be cut"
	s := &Session{Doc: &mapping.Document{Rules: []mapping.Rule{{Name: name}}}}
	c := newCheckView(s)
	c.set([]mapping.Finding{{Rule: 0, Field: field, Message: msg}}, "", "", nil)
	rowAt := func(width int) string {
		for _, l := range strings.Split(plain(c.View(width, 8)), "\n") {
			if strings.Contains(l, "rrrr") {
				return strings.TrimSpace(l)
			}
		}
		return ""
	}
	// 60 columns: the field is cut to nothing, and the message keeps its 20.
	if row := rowAt(60); !strings.Contains(row, name+" › … — "+msg[:20]) {
		t.Fatalf("60 columns: %q", row)
	}
	// A little wider, the field is cut in the middle and keeps its start.
	if row := rowAt(85); !strings.Contains(row, "tuple_filters") || !strings.Contains(row, "…") || !strings.Contains(row, msg[:20]) {
		t.Fatalf("85 columns: %q", row)
	}
	// Narrower than the rule name needs, that is cut too.
	if row := rowAt(40); !strings.Contains(row, "… — "+msg[:20]) {
		t.Fatalf("40 columns: %q", row)
	}
}
