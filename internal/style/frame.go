package style

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Frame renders body inside the wizards' rounded border.
//
// contentWidth is the width of the body, not of the result: lipgloss Width()
// counts the border and padding, so the style is set to contentWidth + 4 for
// the horizontal padding + 2 for the border. Callers size their content and let
// the frame account for its own chrome.
func Frame(body string, contentWidth int) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Subtle).
		Padding(1, 2).
		Width(contentWidth + 6).
		Render(body)
}

// FrameTitled is Frame with text set into its top border, the way a window
// manager sets a title into a title bar: left for where the screen is, right
// for the key that leaves it.
//
// The border is where this belongs rather than the first row inside it. A
// wizard on a 16-row terminal has nine rows of body, and answering "where am I"
// is not worth one of them — the border is already being drawn.
//
// Either legend may be empty, and both are dropped rather than crushed when the
// frame is too narrow to hold them, which leaves an ordinary border.
func FrameTitled(body string, contentWidth int, left, right string) string {
	out := Frame(body, contentWidth)
	top, rest, ok := strings.Cut(out, "\n")
	if !ok {
		return out
	}
	// Measured off the border that was drawn rather than recomputed from
	// contentWidth, so the replacement cannot drift from what it replaces.
	inner := lipgloss.Width(top) - 2
	left, right = fitLegends(inner, left, right)
	if left == "" && right == "" {
		return out
	}
	return frameTop(inner, left, right) + "\n" + rest
}

// fitLegends trims the pair to what inner cells can hold.
//
// The right legend is measured first and never truncated: it is the way out,
// the one affordance that has to survive a narrow terminal, because a user who
// cannot read it is stuck. The left gives up its columns to it, and goes
// entirely below what an ellipsis would itself cost rather than showing as one.
func fitLegends(inner int, left, right string) (string, string) {
	if legendWidth(right)+2 > inner {
		right = ""
	}
	room := inner - 2 - legendWidth(right) - 2
	if lipgloss.Width(left) > room {
		if room < 2 {
			return "", right
		}
		left = ansi.Truncate(left, room, "…")
	}
	return left, right
}

// legendWidth is what a legend occupies on the border, including the space that
// sets it off from the rule on either side.
func legendWidth(s string) int {
	if s == "" {
		return 0
	}
	return lipgloss.Width(s) + 2
}

// frameTop redraws the top border with the legends set into it. inner is the
// span between the two corners, which the segments below fill exactly.
func frameTop(inner int, left, right string) string {
	b := lipgloss.RoundedBorder()
	rule := func(n int) string {
		if n < 1 {
			return ""
		}
		return lipgloss.NewStyle().Foreground(Subtle).Render(strings.Repeat(b.Top, n))
	}
	legend := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}

	corner := lipgloss.NewStyle().Foreground(Subtle)

	// One cell of rule at each end, so a legend never sits against a corner.
	fill := inner - 2 - legendWidth(left) - legendWidth(right)
	return corner.Render(b.TopLeft) +
		rule(1) + legend(left) + rule(fill) + legend(right) + rule(1) +
		corner.Render(b.TopRight)
}
