package mapping

import (
	"regexp"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
)

// hangIndent is how far a wrapped row sits past the indent of the row it
// continues. Two columns is enough to read as "this is still the line above"
// and not so much that a continued value looks like a field nested under it.
const hangIndent = "  "

// wrapLine lays one line out across as many rows as it needs, hanging every row
// after the first past the original's own indent.
//
// It replaces a truncating clamp on the preview, where cutting took the worst
// possible half: an evaluated tuple ends with its object, and the object is the
// part that says which thing the rule touched — the one part the user cannot
// reconstruct from the rule they just wrote. In the document it took the end of
// every template, which is where the field path lives.
//
// The whole line is re-wrapped rather than the first row kept at full width,
// because a line short enough to keep is returned untouched above: anything
// reaching the wrap is overflowing already, and a uniform width costs at most
// the two columns the hang occupies.
func wrapLine(s string, w int) []string {
	if w < 1 || lipgloss.Width(s) <= w {
		return []string{s}
	}
	indent := s[:len(s)-len(strings.TrimLeft(s, " "))]
	hang := indent + hangIndent
	width := w - lipgloss.Width(hang)
	if width < 8 {
		// Narrower than this, the indent is costing more than it explains: a
		// column that fits four characters of content per row has no shape left
		// for the hang to clarify.
		indent, hang, width = "", "", w
	}

	rows := strings.Split(ansi.Wrap(strings.TrimLeft(s, " "), width, ""), "\n")
	for i := range rows {
		if i == 0 {
			rows[i] = indent + rows[i]
			continue
		}
		rows[i] = hang + rows[i]
	}
	return rows
}

// wrapText wraps a block, keeping the lines it already has. mapper's compile
// and evaluation errors arrive laid out across several lines, with a caret
// under the offending column.
func wrapText(s string, w int) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, wrapLine(l, w)...)
	}
	return strings.Join(out, "\n")
}

// wrappedHeight is how many rows a block takes once wrapped to w, which is what
// a section asks the row budget for. An empty block asks for nothing rather than
// for the one row a bare "" would otherwise count as.
func wrappedHeight(s string, w int) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		n += len(wrapLine(l, w))
	}
	return n
}

// jsonKey matches a field at the head of a line: indent, the quoted name, then
// the colon and whatever follows. The quotes are what make this safe where the
// YAML pattern needs a trailing space to be — a value carrying a colon of its
// own cannot reach the front of a line unquoted.
var jsonKey = regexp.MustCompile(`^(\s*)("(?:[^"\\]|\\.)*")(:.*)$`)

// highlightedJSON is the payload half of the preview: the event the rule is
// evaluated against, wrapped to the pane, cut to the rows it was given, and
// coloured.
//
// Only field names are picked out. The values are the reason to read this at
// all — they are what a user checks a path against before writing it into a
// template — so they keep the plain foreground rather than competing with a
// second colour for it.
func highlightedJSON(s string, w, n int) string {
	if n <= 0 {
		return ""
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		for i, row := range wrapLine(line, w) {
			if m := jsonKey.FindStringSubmatch(row); i == 0 && m != nil {
				out = append(out, m[1]+style.Key.Render(m[2])+m[3])
				continue
			}
			out = append(out, row)
		}
	}
	if len(out) > n {
		out = append(out[:n], style.Faint.Render("…"))
	}
	return strings.Join(out, "\n")
}

// yamlKey matches a field at the head of a line: indent, an optional list
// marker, the name, and the colon. The trailing space is required so that a
// value carrying a colon of its own — `user:alice`, which is every tuple the
// wizard writes — is not mistaken for a field.
var yamlKey = regexp.MustCompile(`^(\s*)(- )?([\w.-]+:)( .*)?$`)

// highlightedYAML is the document half of the preview: wrapped to the pane,
// cut to the rows it was given, and coloured.
//
// Colour comes last, after the wrap, so that no escape sequence is ever cut
// through the middle — the reason the old rune-based clamp could not have
// stayed. n <= 0 hides the block entirely, which is how this half yields its
// rows to the evaluation half on a short screen.
func highlightedYAML(s string, w, n int) string {
	if n <= 0 {
		return ""
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		open := false
		for i, row := range wrapLine(line, w) {
			var coloured string
			coloured, open = highlightYAML(row, i == 0, open)
			out = append(out, coloured)
		}
	}
	if len(out) > n {
		out = append(out[:n], style.Faint.Render("…"))
	}
	return strings.Join(out, "\n")
}

// highlightYAML colours one rendered row. first says the row starts a line
// rather than continuing one, which is what makes it safe to read a field name
// off the front of it; open says the row begins inside a template that started
// on an earlier row, and the returned flag says the same about the next.
//
// Colour is decoration only: stripping the escapes gives back exactly the row
// that came in.
func highlightYAML(row string, first, open bool) (string, bool) {
	if !open {
		if strings.HasPrefix(strings.TrimLeft(row, " "), "#") {
			return style.Faint.Render(row), false
		}
		if m := yamlKey.FindStringSubmatch(row); first && m != nil {
			rest, stillOpen := highlightTemplates(m[4], false)
			return m[1] + faintly(m[2]) + style.Key.Render(m[3]) + rest, stillOpen
		}
	}
	return highlightTemplates(row, open)
}

// highlightTemplates picks the {{ }} expressions out of the literal text around
// them: they are the only part of the file that runs, and the only part whose
// mistakes the preview beside it is reporting.
//
// A template long enough to wrap arrives here in halves, which is what open
// carries — colouring only the half holding the braces would read as the
// expression ending mid-row.
func highlightTemplates(s string, open bool) (string, bool) {
	var b strings.Builder
	for s != "" {
		if open {
			i := strings.Index(s, "}}")
			if i < 0 {
				b.WriteString(templateStyle.Render(s))
				return b.String(), true
			}
			b.WriteString(templateStyle.Render(s[:i+2]))
			s, open = s[i+2:], false
			continue
		}
		i := strings.Index(s, "{{")
		if i < 0 {
			break
		}
		b.WriteString(s[:i])
		s, open = s[i:], true
	}
	b.WriteString(s)
	return b.String(), open
}

var templateStyle = lipgloss.NewStyle().Foreground(style.Keyword)

// faintly renders a fragment that is often empty — the list marker — without
// wrapping nothing in escapes.
func faintly(s string) string {
	if s == "" {
		return ""
	}
	return style.Faint.Render(s)
}
