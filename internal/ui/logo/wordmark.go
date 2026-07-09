package logo

import (
	"math"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/sergiught/openfga-cli/internal/style"
)

// wordmarkGlyphs holds the 5-row, 5-column quadrant-rounded block letterforms
// for the stacked OPENFGA wordmark (design pick "B2+"). Each row is exactly 5
// display columns wide.
var wordmarkGlyphs = map[rune][]string{
	'O': {"▟███▙", "█   █", "█   █", "█   █", "▜███▛"},
	'P': {"████▙", "█   █", "████▛", "█    ", "█    "},
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'N': {"█   █", "██  █", "█ █ █", "█  ██", "█   █"},
	'F': {"█████", "█    ", "████ ", "█    ", "█    "},
	'G': {"▟███▙", "█    ", "█  ██", "█   █", "▜███▛"},
	'A': {"▟███▙", "█   █", "█████", "█   █", "█   █"},
}

// wordRows renders one word as 5 rows of block art, glyphs joined by a blank
// column.
func wordRows(word string) []string {
	rows := make([]string, 5)
	for i, ch := range word {
		g := wordmarkGlyphs[ch]
		for r := 0; r < 5; r++ {
			if i > 0 {
				rows[r] += " "
			}
			rows[r] += g[r]
		}
	}
	return rows
}

// wordmarkArt returns the stacked OPEN / FGA block art, every line padded to a
// uniform width so the per-column gradient aligns across rows.
func wordmarkArt() []string {
	top := wordRows("OPEN")
	bot := wordRows("FGA")
	w := 0
	for _, r := range append(append([]string{}, top...), bot...) {
		if lw := lipgloss.Width(r); lw > w {
			w = lw
		}
	}
	pad := func(rows []string) {
		for i, r := range rows {
			if d := w - lipgloss.Width(r); d > 0 {
				rows[i] = r + strings.Repeat(" ", d)
			}
		}
	}
	pad(top)
	pad(bot)
	lines := append([]string{}, top...)
	lines = append(lines, strings.Repeat(" ", w))
	lines = append(lines, bot...)
	return lines
}

// WordmarkSize returns the wordmark's (columns, rows).
func WordmarkSize() (int, int) {
	lines := wordmarkArt()
	w := 0
	for _, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w, len(lines)
}

// Wordmark renders the stacked OPENFGA block wordmark with a left-to-right
// brand gradient. phase < 0 is static; phase in [0,1] sweeps an entrance
// highlight band across it. Mono/no-gradient themes render the block art
// uncolored (NO_COLOR-clean).
func Wordmark(phase float64) string {
	lines := wordmarkArt()
	if style.Active.Name == "mono" || style.Active.GradStartHex == "" || style.Active.GradEndHex == "" {
		return strings.Join(lines, "\n")
	}
	c1, err1 := colorful.Hex(style.Active.GradStartHex)
	c2, err2 := colorful.Hex(style.Active.GradEndHex)
	if err1 != nil || err2 != nil {
		return strings.Join(lines, "\n")
	}
	maxW, _ := WordmarkSize()
	var b strings.Builder
	for li, line := range lines {
		col := 0
		for _, ch := range line {
			if ch == ' ' {
				b.WriteString(" ")
				col++
				continue
			}
			t := 0.0
			if maxW > 1 {
				t = float64(col) / float64(maxW-1)
			}
			c := c1.BlendLab(c2, t).Clamped()
			if phase >= 0 {
				if d := math.Abs(t - phase); d < 0.18 {
					k := (0.18 - d) / 0.18 * 0.6
					c = c.BlendLuv(colorful.Color{R: 1, G: 1, B: 1}, k).Clamped()
				}
			}
			b.WriteString(lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color(c.Hex())).Render(string(ch)))
			col++
		}
		if li < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
