package playground

import (
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/style"
)

// editorPane renders the DSL editor as a single syntax-colored pane, windowed
// to the visible rows starting at m.editorTop, with a line-number gutter (red
// ▸ marker on diagnostic lines), a reverse-video cursor block when focused,
// and the diagnostic column styled red. Long lines truncate to display width.
func (m Model) editorPane(width, height int) string {
	lines := splitCellLines(dsl.Cells(m.editor.Value()))

	diagByLine := make(map[int]dsl.Diagnostic, len(m.editorDiags))
	for _, d := range m.editorDiags {
		if _, seen := diagByLine[d.Line]; !seen {
			diagByLine[d.Line] = d
		}
	}

	gutterW := len(strconv.Itoa(len(lines)))
	if gutterW < 1 {
		gutterW = 1
	}
	// gutter marker(1) + number(gutterW) + separator(1).
	textW := width - gutterW - 2
	if textW < 1 {
		textW = 1
	}

	focused := m.editor.Focused()
	cursorLine := m.editor.Line()
	li := m.editor.LineInfo()
	cursorCol := li.StartColumn + li.ColumnOffset

	top := m.editorTop

	rows := make([]string, 0, height)
	for i := 0; i < height && top+i < len(lines); i++ {
		row := top + i
		showCursor := focused && row == cursorLine
		errCol := -1
		d, hasDiag := diagByLine[row]
		if hasDiag {
			errCol = d.Col
		}
		gutter := renderGutter(row+1, gutterW, hasDiag)
		rows = append(rows, gutter+" "+renderLine(lines[row], textW, showCursor, cursorCol, errCol))
	}

	return strings.Join(rows, "\n")
}

// splitCellLines splits a cell stream into logical lines, dropping newline
// cells. Always returns at least one (possibly empty) line.
func splitCellLines(cells []dsl.Cell) [][]dsl.Cell {
	lines := [][]dsl.Cell{{}}
	for _, c := range cells {
		if c.R == '\n' {
			lines = append(lines, []dsl.Cell{})
			continue
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], c)
	}
	return lines
}

// renderGutter renders a line number, right-aligned in numW, prefixed by a red
// marker on diagnostic lines (a space otherwise) so columns stay aligned.
func renderGutter(lineNum, numW int, hasDiag bool) string {
	num := strconv.Itoa(lineNum)
	if pad := numW - len(num); pad > 0 {
		num = strings.Repeat(" ", pad) + num
	}
	if hasDiag {
		return style.Failure.Render("▸" + num)
	}
	return " " + lipgloss.NewStyle().Foreground(style.Muted).Render(num)
}

// renderLine renders one logical line's cells up to width visual columns,
// coloring each rune, marking the error column red, and drawing a
// reverse-video cursor block at cursorCol when showCursor is set (a trailing
// block cursor sits past the last rune).
func renderLine(cells []dsl.Cell, width int, showCursor bool, cursorCol, errCol int) string {
	cursor := lipgloss.NewStyle().Reverse(true)
	errStyle := lipgloss.NewStyle().Foreground(style.Red).Underline(true)

	var b strings.Builder
	drawn := 0
	for j, c := range cells {
		if drawn >= width {
			break
		}
		text := style.SanitizeTerminal(string(c.R))
		if text == "" {
			continue
		}
		s := lipgloss.NewStyle()
		if c.Color != nil {
			s = s.Foreground(c.Color)
		}
		switch {
		case showCursor && j == cursorCol:
			s = cursor
		case j == errCol:
			s = errStyle
		}
		b.WriteString(s.Render(text))
		drawn++
	}
	if showCursor && cursorCol >= len(cells) && drawn < width {
		b.WriteString(cursor.Render(" "))
	}
	// If cursorCol is at or beyond width, drawn == width and the cursor block
	// above is skipped: a consequence of the v1 no-horizontal-scroll design,
	// not a bug.
	return b.String()
}
