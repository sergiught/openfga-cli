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
