// Package logo renders the ofga wordmark as block-letter art, in the spirit of
// charmbracelet/crush's stylized title. The art is plain; callers apply the
// brand gradient (e.g. style.GradientBlock) and surrounding decoration.
package logo

import "strings"

// glyphs holds 5-row solid-block letterforms for the wordmark.
var glyphs = map[rune][]string{
	'o': {
		" ███ ",
		"█   █",
		"█   █",
		"█   █",
		" ███ ",
	},
	'f': {
		"█████",
		"█    ",
		"███  ",
		"█    ",
		"█    ",
	},
	'g': {
		" ████",
		"█    ",
		"█  ██",
		"█   █",
		" ███ ",
	},
	'a': {
		" ███ ",
		"█   █",
		"█████",
		"█   █",
		"█   █",
	},
}

// Height is the number of rows in the block art.
const Height = 5

// Word renders s as block-letter art (plain text, no color). Each rendered line
// is padded to a uniform width so a per-line gradient aligns across rows.
// Unknown runes are skipped.
func Word(s string) string {
	rows := make([]string, Height)
	first := true
	for _, r := range s {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for i := 0; i < Height; i++ {
			if !first {
				rows[i] += " "
			}
			rows[i] += g[i]
		}
		first = false
	}
	return strings.Join(rows, "\n")
}
