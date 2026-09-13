package style

import lipgloss "charm.land/lipgloss/v2"

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
