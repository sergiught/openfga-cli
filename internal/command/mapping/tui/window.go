package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// windowAround returns at most h of lines, keeping line cursor in view, with
// "↑ N more" / "↓ N more" in place of the first or last visible line when
// lines are hidden above or below. h < 3 shows the cursor line alone.
//
// The host pane never scrolls or clips a component, so a component taller than
// its height has to cut itself down, and say what it cut.
func windowAround(lines []string, cursor, h int) string {
	n := len(lines)
	if n == 0 || h < 1 {
		return ""
	}
	cursor = max(0, min(cursor, n-1))
	if n <= h {
		return strings.Join(lines, "\n")
	}
	if h < 3 {
		return lines[cursor]
	}
	start := max(0, min(cursor-(h-1)/2, n-h))
	// An indicator takes over the edge line, so the cursor must sit clear of it.
	for range 3 {
		end := start + h
		switch {
		case start > 0 && cursor <= start:
			start = max(0, cursor-1)
		case end < n && cursor >= end-1:
			start = min(n-h, cursor+2-h)
		}
	}
	end := start + h
	out := append([]string(nil), lines[start:end]...)
	if start > 0 {
		out[0] = moreMark("↑", start+1)
	}
	if end < n {
		out[len(out)-1] = moreMark("↓", n-end+1)
	}
	return strings.Join(out, "\n")
}

func moreMark(arrow string, n int) string {
	return lipgloss.NewStyle().Foreground(style.Faintc).Render(fmt.Sprintf("%s %d more", arrow, n))
}

// pickerWindow draws p within height rows around its cursor. It finds the
// cursor's line by looking for the marker the picker draws, since a row with a
// description spans two lines.
func pickerWindow(p *picker.Picker, width, height int) string {
	lines := strings.Split(p.View(width), "\n")
	cursor := 0
	for i, l := range lines {
		if strings.HasPrefix(ansi.Strip(l), "▌") {
			cursor = i
			break
		}
	}
	return windowAround(lines, cursor, height)
}
