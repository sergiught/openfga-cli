package mapping

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// wrapLine is the whole answer to the complaint that started this: a tuple's
// object — the part that says which thing the rule touched — sat at the end of
// the line and so was the part the ellipsis ate.
func TestWrapLineHangsContinuationsUnderTheirOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		w    int
		want []string
	}{
		{"a line that fits is left alone", "user:acme", 20, []string{"user:acme"}},
		{
			"a flush line hangs two columns in",
			"write user:alice member organization:acme",
			20,
			[]string{"write user:alice", "  member", "  organization:acme"},
		},
		{
			"an indented line hangs past its own indent",
			"    object: organization:acme",
			26,
			[]string{"    object:", "      organization:acme"},
		},
		{
			"a word longer than the width is broken rather than dropped",
			"  id: aaaaaaaaaaaaaaaaaaaaaaaa",
			16,
			[]string{"  id:", "    aaaaaaaaaaaa", "    aaaaaaaaaaaa"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapLine(tc.in, tc.w)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("wrapLine(%q, %d) =\n%q\nwant\n%q", tc.in, tc.w, got, tc.want)
			}
			for _, l := range got {
				if lipgloss.Width(l) > tc.w {
					t.Fatalf("row %q is %d cells, over the %d it was given", l, lipgloss.Width(l), tc.w)
				}
			}
		})
	}
}

// The evaluated tuple is the line the user complained about by name. It is
// built from their own event payload, so the object is the one part of it they
// cannot reconstruct from the rule they wrote.
func TestTheEvaluatedTupleIsWrappedNotCut(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(m.preview.Tuples) == 0 {
		t.Fatal("the sample produced no tuples, so there is nothing to wrap")
	}
	pane := plain(previewOf(m))
	for _, want := range []string{m.preview.Tuples[0].User, m.preview.Tuples[0].Object} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the preview lost %q:\n%s", want, pane)
		}
	}
}

// Wrapping is only an improvement while it stays inside the column it was given:
// a row that overflows the pane lands in the editor beside it.
func TestNoPreviewRowOverflowsThePane(t *testing.T) {
	for _, w := range []int{100, 120, 160} {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		pw := m.previewWidth()
		for _, l := range strings.Split(plain(previewOf(m)), "\n") {
			if lipgloss.Width(l) > pw {
				t.Fatalf("at %d cols the row %q is %d cells, over the pane's %d",
					w, l, lipgloss.Width(l), pw)
			}
		}
	}
}

// The document reads as a document rather than as a wall: keys are picked out
// from their values, and the {{ }} templates — the only part of the file that
// runs — from the literal text around them.
func TestTheDocumentPreviewIsHighlighted(t *testing.T) {
	out := highlightedYAML("rules:\n  - user: \"user:{{ input.id }}\"\n", 60, 10)
	for _, want := range []string{
		style.Key.Render("rules:"),
		style.Key.Render("user:"),
		lipgloss.NewStyle().Foreground(style.Keyword).Render("{{ input.id }}"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the document is not highlighted: %q missing from %q", want, out)
		}
	}
}

// Colour is decoration over the file, never a change to it — the same contract
// internal/dsl's highlighter keeps. A preview that quietly rewrote the YAML
// would be worse than one with no colour at all.
func TestHighlightingLeavesTheTextAlone(t *testing.T) {
	src := "version: \"1\"\nrules:\n  # a comment\n  - name: x\n    when: input.type == \"x\"\n"
	got := plain(highlightedYAML(src, 60, 10))
	if want := strings.TrimRight(src, "\n"); got != want {
		t.Fatalf("highlighting changed the text:\ngot  %q\nwant %q", got, want)
	}
}

// A template long enough to wrap is split across two rows, and both halves are
// still part of the same expression. Colouring only the half that carries the
// braces would read as the expression ending mid-line.
func TestASplitTemplateStaysHighlightedOnBothRows(t *testing.T) {
	out := highlightedYAML("  object: \"organization:{{ input.data.object.organization.id }}\"", 30, 10)
	rows := strings.Split(out, "\n")
	if len(rows) < 2 {
		t.Fatalf("the line did not wrap, so there is nothing to carry over: %q", out)
	}
	open, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(style.Keyword).Render("x"), "x")
	for i, r := range rows[:2] {
		if !strings.Contains(r, open) {
			t.Fatalf("row %d of the split template carries no colour: %q", i, r)
		}
	}
}

// The budget the YAML half is given is in rows, and wrapping spends rows — so
// the block must still stop where it was told to.
func TestTheDocumentStopsAtTheRowsItWasGiven(t *testing.T) {
	src := strings.Repeat("  - name: a name long enough that it has to wrap somewhere\n", 20)
	if h := lipgloss.Height(highlightedYAML(src, 30, 6)); h != 7 {
		t.Fatalf("the document block is %d rows, want its 6 plus the … row", h)
	}
}

// The payload is the third thing the pane shows. Writing
// `{{ input.data.object.organization.id }}` means knowing what the event holds,
// and ^p inserts one path without ever showing the shape they come from.
func TestThePayloadIsShownBesideTheMapping(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	pane := plain(previewOf(m))
	for _, want := range []string{"event", `"a0tenant"`, `"org_1234567890abcdef"`} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the payload section is missing %q:\n%s", want, pane)
		}
	}
}

// Inside an iterator the expressions address the item, not the event:
// `identity.connection`, never `input.data.object.identities[0].connection`.
// Showing the whole event there would point the user at paths that do not
// resolve — the same rewrite the path picker already does.
func TestInsideAnIteratorThePayloadIsTheItem(t *testing.T) {
	m := atRuleFor(t, "user.created")
	m.inIter = true

	label, payload := m.samplePayload()
	if label != "identity" {
		t.Fatalf("the payload is headed %q, want the iterator's alias", label)
	}
	if !strings.Contains(payload, `"connection"`) {
		t.Fatalf("the item's own fields are missing:\n%s", payload)
	}
	if strings.Contains(payload, "a0tenant") {
		t.Fatalf("the whole event is shown, whose paths do not resolve here:\n%s", payload)
	}
}

// The payload is reference material beside the file being written, so it is the
// section that gives up its rows first. The document and the evaluation — the
// wizard's promise, and the thing the user is reacting to — both outlive it.
//
// The assertion is on the rendered view rather than the pane, because the pane
// is drawn into a MaxHeight container: a budget that promises more rows than
// the pane has is not an overflow, it is the evaluation silently clipped off
// the bottom.
func TestThePayloadYieldsItsRowsBeforeTheOthers(t *testing.T) {
	var lost int
	for _, h := range []int{60, 40, 30, 24, 20, minRows} {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		view := plain(m.viewString())

		if !strings.Contains(view, "preview") {
			t.Errorf("at 120x%d the payload cost the evaluation its place:\n%s", h, view)
		}
		if !strings.Contains(view, m.path) {
			t.Errorf("at 120x%d the document went before the payload:\n%s", h, view)
		}
		if !strings.Contains(plain(previewOf(m)), "event \u2500") {
			lost++
		}
	}
	if lost == 0 {
		t.Fatal("the payload never yielded, so this proves nothing about the order")
	}
}

// The budget is what stands between three sections and a pane that promises
// more rows than it has. It is checked against the renderers themselves rather
// than against a model of them, because the off-by-one that matters is the
// ellipsis row a truncated section adds.
func TestPreviewBudgetFitsTheRowsItWasGiven(t *testing.T) {
	const w = 50
	for _, tc := range []struct{ rows, eval, doc, payload int }{
		{40, 4, 12, 60},  // a long payload against a short document
		{40, 4, 60, 60},  // both longer than the pane
		{30, 10, 12, 60}, // a talkative evaluation
		{24, 8, 12, 60},
		{20, 12, 12, 60}, // nothing left for either
		{40, 4, 5, 5},    // both short, room to spare
	} {
		doc := strings.Repeat("key: value\n", tc.doc)
		payload := strings.Repeat("  \"key\": \"value\",\n", tc.payload)

		docRows, payloadRows := previewBudget(tc.rows, tc.eval,
			wrappedHeight(doc, w), wrappedHeight(payload, w))

		used := tc.eval + 1 // the evaluation and its header
		if docRows > 0 {
			used += lipgloss.Height(highlightedYAML(doc, w, docRows)) + 2
		}
		if payloadRows > 0 {
			used += lipgloss.Height(highlightedJSON(payload, w, payloadRows)) + 2
		}
		if used > tc.rows {
			t.Errorf("rows=%d eval=%d doc=%d payload=%d: the budget spends %d of %d",
				tc.rows, tc.eval, tc.doc, tc.payload, used, tc.rows)
		}
	}
}

// Three sections sharing the rows two had is only safe while the screen still
// fits the terminal. The general overflow sweep drives screens by stack alone,
// so it never has a sample and never renders a payload section at all.
func TestTheScreenWithAPayloadStaysInsideTheTerminal(t *testing.T) {
	for _, sz := range layoutSizes {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		if _, payload := m.samplePayload(); payload == "" {
			t.Fatal("this rule has no sample, so the test exercises nothing")
		}
		if why := doesNotFit(m.viewString(), sz.w, sz.h); why != "" {
			t.Errorf("at %dx%d: %s:\n%s", sz.w, sz.h, why, m.viewString())
		}
	}
}

// Colour over the payload is decoration only, the same contract the document
// half keeps: stripping the escapes gives back the JSON that went in.
func TestHighlightingLeavesThePayloadAlone(t *testing.T) {
	src := "{\n  \"type\": \"user.created\",\n  \"data\": {\n    \"id\": 7\n  }\n}"
	if got := plain(highlightedJSON(src, 60, 20)); got != src {
		t.Fatalf("highlighting changed the payload:\ngot  %q\nwant %q", got, src)
	}
	if !strings.Contains(highlightedJSON(src, 60, 20), style.Key.Render(`"type"`)) {
		t.Fatal("the field names are not picked out from their values")
	}
}
