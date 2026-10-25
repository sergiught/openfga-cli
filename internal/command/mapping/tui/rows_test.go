package tui

import (
	"slices"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
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

// The document reads as a document rather than as a wall: keys are picked out
// from their values, and the {{ }} templates — the only part of the file that
// runs — from the literal text around them.
// posixWords splits s the way a POSIX shell does for the commands wrapCommand
// sees: '…' is literal (a backslash and newline inside it stay), outside it a
// backslash escapes the next character (and with a newline, vanishes), and
// adjacent pieces concatenate into one word.
func posixWords(s string) []string {
	var words []string
	var cur strings.Builder
	has, quoted := false, false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quoted:
			if r == '\'' {
				quoted = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			quoted, has = true, true
		case r == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				cur.WriteRune(rs[i])
				has = true
			}
		case r == ' ' || r == '\n' || r == '\t':
			if has {
				words = append(words, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		words = append(words, cur.String())
	}
	return words
}

// A shell joins a "\" and its newline away, but not inside '…': what
// wrapCommand lays out must, read by a shell, be the command it was given, and
// no row but the last may end it.
func TestWrapCommandRunsOnlyWhole(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		w         int
	}{
		{"words", "ofga mappings apply m.yaml --events e.jsonl --from-event 2 --store-id 01ARZ3NDEKTSV4RRFFQ69G5FAV", 30},
		{"a quoted path stays whole", "ofga mappings apply 'my map.yaml' --events EVENTS_FILE --from-event 2", 26},
		{"a word wider than a row", "ofga mappings apply /tmp/a/very/long/directory/name/that/goes/on/mapping.yaml --from-event 3", 24},
		{"an escaped quote is not cut from its backslash", "ofga mappings apply '/tmp/it'\\''s/a/long/name.yaml' --from-event 3", 14},
		{"a long quoted path", "ofga mappings apply '/tmp/a very/long directory/name/that/goes/on/mapping.yaml' --from-event 3", 24},
		{"a quoted path holding a quote", "ofga mappings apply '/tmp/it'\\''s/a long/name.yaml' --from-event 3", 20},
		{"a narrow width", "ofga mappings apply '/tmp/a b/c.yaml' --from-event 3", 12},
		{"no row holds only the closing quote", "ofga mappings apply --profile 'my prof'", 12},
		{"a word that just fills a row", "ab 12345678901234567 cd", 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := wrapCommand(tc.cmd, tc.w)
			if len(rows) < 2 {
				t.Fatalf("not wrapped: %q", rows)
			}
			for i, r := range rows {
				if lipgloss.Width(r) > tc.w {
					t.Errorf("row %d is %d cells, want at most %d: %q", i, lipgloss.Width(r), tc.w, r)
				}
				if strings.TrimSuffix(r, `\`) == "''" || strings.TrimSpace(strings.TrimSuffix(r, `\`)) == "'" {
					t.Errorf("row %d holds only a closing quote: %q", i+1, r)
				}
				last := i == len(rows)-1
				if ends := strings.HasSuffix(r, `\`); ends == last || strings.HasSuffix(r, `\\`) {
					t.Errorf("row %d of %d: %q", i+1, len(rows), r)
				}
			}
			if got, want := posixWords(strings.Join(rows, "\n")), posixWords(tc.cmd); !slices.Equal(got, want) {
				t.Fatalf("a shell reads %q, want %q (rows %q)", got, want, rows)
			}
			if tc.name == "a quoted path stays whole" {
				for _, whole := range []string{"'my map.yaml'", "EVENTS_FILE"} {
					if !strings.Contains(strings.Join(rows, "\n"), whole) {
						t.Errorf("%s was cut: %q", whole, rows)
					}
				}
			}
		})
	}
	if rows := wrapCommand("ofga mappings apply m.yaml", 40); len(rows) != 1 {
		t.Fatalf("a command that fits was wrapped: %q", rows)
	}
}

func TestTheDocumentPreviewIsHighlighted(t *testing.T) {
	out := strings.Join(yamlRows("rules:\n  - user: \"user:{{ input.id }}\"\n", 60), "\n")
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
	got := plain(strings.Join(yamlRows(src, 60), "\n"))
	if want := strings.TrimRight(src, "\n"); got != want {
		t.Fatalf("highlighting changed the text:\ngot  %q\nwant %q", got, want)
	}
}

// A template long enough to wrap is split across two rows, and both halves are
// still part of the same expression. Colouring only the half that carries the
// braces would read as the expression ending mid-line.
func TestASplitTemplateStaysHighlightedOnBothRows(t *testing.T) {
	rows := yamlRows("  object: \"organization:{{ input.data.object.organization.id }}\"", 30)
	if len(rows) < 2 {
		t.Fatalf("the line did not wrap, so there is nothing to carry over: %q", rows)
	}
	open, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(style.Keyword).Render("x"), "x")
	for i, r := range rows[:2] {
		if !strings.Contains(r, open) {
			t.Fatalf("row %d of the split template carries no colour: %q", i, r)
		}
	}
}

// Colour over the payload is decoration only, the same contract the document
// half keeps: stripping the escapes gives back the JSON that went in.
func TestHighlightingLeavesThePayloadAlone(t *testing.T) {
	src := "{\n  \"type\": \"user.created\",\n  \"data\": {\n    \"id\": 7\n  }\n}"
	rows := strings.Join(jsonRows(src, 60), "\n")
	if got := plain(rows); got != src {
		t.Fatalf("highlighting changed the payload:\ngot  %q\nwant %q", got, src)
	}
	if !strings.Contains(rows, style.Key.Render(`"type"`)) {
		t.Fatal("the field names are not picked out from their values")
	}
}

// hostile carries a screen clear, a C1 control and a bidi override, the three
// things a payload or a hand-edited file can use to drive the terminal.
const hostile = "x\x1b[2Jy\u009bz\u202ew"

func TestRowsFromAFileCannotDriveTheTerminal(t *testing.T) {
	doc := func() *mapping.Document {
		d := identitiesDoc()
		r := &d.Rules[0]
		r.Name = hostile
		r.When = hostile
		r.Action = hostile
		r.Variables = []mapping.Variable{{Name: "org", Expr: hostile}}
		r.Filters = []mapping.TupleFilter{{User: hostile, Relation: hostile, Object: hostile}}
		r.Tuples = []mapping.Tuple{{User: hostile, Relation: hostile, Object: hostile}}
		r.Iterator.Source = hostile
		r.Iterator.Tuples = []mapping.Tuple{{User: hostile, Relation: hostile, Object: hostile}}
		d.Tests = []mapping.TestCase{{Name: hostile, Input: map[string]any{"type": hostile}}}
		return d
	}
	for name, open := range map[string]func(s *Session) component{
		"hub":           openHub,
		"spoke":         openSpoke,
		"tuple list":    openTuples,
		"iter tuples":   func(s *Session) component { return newTupleList(s, Focus{InIter: true}) },
		"filter list":   openFilters,
		"iterator hub":  openIter,
		"variable list": openVars,
		"test list":     openTests,
		"test editor":   openTestEditor,
		"confirm":       func(s *Session) component { return newConfirmDelete(s, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, doc(), nil, open)
			out := h.view(120, 30)
			if hd, ok := h.top().(headed); ok {
				title, sub := hd.heading()
				out += title + sub
			}
			for _, bad := range []string{"\x1b[2J", "\u009b", "\u202e"} {
				if strings.Contains(out, bad) {
					t.Errorf("%q reached the screen:\n%q", bad, out)
				}
			}
		})
	}

	d := doc()
	yaml, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range yamlRows(string(yaml), 120) {
		for _, bad := range []string{"\x1b[2J", "\u009b", "\u202e"} {
			if strings.Contains(row, bad) {
				t.Errorf("%q reached a preview row: %q", bad, row)
			}
		}
	}
}
