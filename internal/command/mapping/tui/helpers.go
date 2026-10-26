package tui

import (
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// Glyph prefixes text with an icon and a space, or leaves it alone when the
// icon rung has none: under icons = off a decorative icon is empty, and a bare
// leading space would read as an indent.
func Glyph(ic, text string) string {
	if ic == "" {
		return text
	}
	return ic + " " + text
}

// ProblemMark is the one place a severity becomes a character. A model warning
// is not a failure — the mapping saves — but it is not a clean bill either.
func ProblemMark(p mapping.Problem) string {
	if p.Warning {
		return "!"
	}
	return "✗"
}

// emptyState renders "nothing here yet" plus the one key that fixes it, with
// the key picked out so it reads as an instruction rather than prose.
func emptyState(what, key, action string) string {
	muted := style.Subtitle
	return muted.Render(what) + "\n\n" +
		muted.Render("Press") + style.Keycap(key) + muted.Render("to "+action+".")
}

// listOrEmpty renders l, or its empty state when it has no items. A read-only
// session refuses the key that would fix the emptiness, so it gets no hint.
func listOrEmpty(l *uilist.List, readOnly bool, what, key, action string) string {
	if len(l.Model.Items()) == 0 {
		if readOnly {
			return style.Subtitle.Render(what)
		}
		return emptyState(what, key, action)
	}
	return l.View()
}
