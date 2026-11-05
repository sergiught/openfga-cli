package optimize

import (
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// UnifiedDiff diffs before/after line by line, labelled with the model file. go-udiff's
// own renderer emits a wrong +start on every hunk after the first, so the
// headers are recomputed here from the hunk lines.
func UnifiedDiff(file, before, after string) string {
	edits := udiff.Lines(before, after)
	u, err := udiff.ToUnifiedDiff(file, file+" (optimized)", before, edits, udiff.DefaultContextLines)
	if err != nil {
		return udiff.Unified(file, file+" (optimized)", before, after)
	}
	if len(u.Hunks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", u.From, u.To)
	delta := 0
	for _, h := range u.Hunks {
		oldN, newN := 0, 0
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Delete:
				oldN++
			case udiff.Insert:
				newN++
			default:
				oldN++
				newN++
			}
		}
		oldStart := h.FromLine
		newStart := oldStart + delta
		delta += newN - oldN
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldN), hunkRange(newStart, newN))
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Delete:
				b.WriteString("-")
			case udiff.Insert:
				b.WriteString("+")
			default:
				b.WriteString(" ")
			}
			b.WriteString(l.Content)
			if !strings.HasSuffix(l.Content, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return b.String()
}

// hunkRange formats a start,count pair; an empty side is "start-1,0" per
// the GNU convention and a single line omits the count.
func hunkRange(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start-1)
	case 1:
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
