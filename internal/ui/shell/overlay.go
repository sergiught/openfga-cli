package shell

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Dialog renders a centered-title rounded box with an opaque panel background,
// suitable for floating over the shell via Overlay.
func Dialog(title, body string, width int) string {
	if width < 8 {
		width = 8
	}
	inner := width - 4 // border (2) + padding (2)
	head := lipgloss.PlaceHorizontal(inner, lipgloss.Center,
		lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(title))
	content := head + "\n\n" + body
	return lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Primary).
		Background(style.BgRaised).
		Padding(0, 1).
		Render(content)
}

// Overlay composites dialog centered on top of base, preserving base's width
// (w) and height (h). Cells covered by the dialog are replaced entirely; the
// surrounding base content shows through unchanged.
func Overlay(base, dialog string, w, h int) string {
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < h {
		baseLines = append(baseLines, "")
	}
	dlgLines := strings.Split(dialog, "\n")
	dw := lipgloss.Width(dialog)
	dh := len(dlgLines)

	top := (h - dh) / 2
	if top < 0 {
		top = 0
	}
	left := (w - dw) / 2
	if left < 0 {
		left = 0
	}

	for i, dl := range dlgLines {
		row := top + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		baseLines[row] = spliceLine(baseLines[row], dl, left, dw, w)
	}
	return strings.Join(baseLines[:h], "\n")
}

// spliceLine overwrites `dw` columns of base starting at column `left` with
// seg, padding as needed and preserving the base tail. ANSI-aware.
func spliceLine(base, seg string, left, dw, total int) string {
	prefix := ansi.Truncate(base, left, "")
	if pw := ansi.StringWidth(prefix); pw < left {
		prefix += strings.Repeat(" ", left-pw)
	}
	suffix := ansi.TruncateLeft(base, left+dw, "")
	line := prefix + seg + suffix
	if lw := ansi.StringWidth(line); lw > total {
		line = ansi.Truncate(line, total, "")
	}
	return line
}
