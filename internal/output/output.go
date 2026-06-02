// Package output renders command results either as a styled table/summary for
// humans or as indented JSON for machines (--json).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sergiught/openfga-cli/internal/style"
)

// Output mode toggles set from global flags before commands run.
var (
	// Quiet suppresses incidental success/info lines (-q/--quiet).
	Quiet bool
	// Plain renders tables as tab-separated, unstyled rows (--plain).
	Plain bool
)

// JSON writes v as indented JSON to w.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Table renders a simple, aligned table with a styled header. Columns are
// sized to their widest cell. It is intentionally dependency-light so it can be
// used from any command. In Plain mode it emits tab-separated, unstyled rows
// for grep/awk pipelines.
func Table(w io.Writer, headers []string, rows [][]string) {
	if Plain {
		for _, row := range rows {
			fmt.Fprintln(w, strings.Join(row, "\t"))
		}
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if cw := lipgloss.Width(cell); cw > widths[i] {
					widths[i] = cw
				}
			}
		}
	}

	cell := func(s string, i int) string {
		pad := widths[i] - lipgloss.Width(s)
		if pad < 0 {
			pad = 0
		}
		return s + strings.Repeat(" ", pad)
	}

	// Header.
	var hb strings.Builder
	for i, h := range headers {
		if i > 0 {
			hb.WriteString("   ")
		}
		hb.WriteString(style.TableHeader.Render(cell(h, i)))
	}
	fmt.Fprintln(w, hb.String())

	// Rule.
	var rb strings.Builder
	for i := range headers {
		if i > 0 {
			rb.WriteString("   ")
		}
		rb.WriteString(style.Faint.Render(strings.Repeat("─", widths[i])))
	}
	fmt.Fprintln(w, rb.String())

	// Rows.
	for _, row := range rows {
		var b strings.Builder
		for i := range headers {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			if i > 0 {
				b.WriteString("   ")
			}
			b.WriteString(cell(val, i))
		}
		fmt.Fprintln(w, b.String())
	}
}

// KeyValues renders an aligned key/value block (used for "get" style output).
func KeyValues(w io.Writer, pairs [][2]string) {
	width := 0
	for _, p := range pairs {
		if lipgloss.Width(p[0]) > width {
			width = lipgloss.Width(p[0])
		}
	}
	for _, p := range pairs {
		pad := strings.Repeat(" ", width-lipgloss.Width(p[0]))
		fmt.Fprintf(w, "%s%s  %s\n", style.Key.Render(p[0]), pad, style.Value.Render(p[1]))
	}
}

// Successf prints a success line with a green check (suppressed in Quiet/Plain).
func Successf(w io.Writer, format string, a ...any) {
	if Quiet || Plain {
		return
	}
	fmt.Fprintf(w, "%s %s\n", style.Success.Render(style.IconCheck), fmt.Sprintf(format, a...))
}

// Infof prints a muted informational line with a bullet (suppressed in Quiet/Plain).
func Infof(w io.Writer, format string, a ...any) {
	if Quiet || Plain {
		return
	}
	fmt.Fprintf(w, "%s %s\n", style.Bullet(), fmt.Sprintf(format, a...))
}

// Title prints a bold violet title line.
func Title(w io.Writer, s string) {
	fmt.Fprintln(w, style.Title.Render(s))
}
