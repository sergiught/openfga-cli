package model

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/style"
)

// unifiedDiff diffs before/after, labelled with the model file. go-udiff's
// own renderer emits a wrong +start on every hunk after the first, so the
// headers are recomputed here from the hunk lines.
func unifiedDiff(file, before, after string) string {
	edits := udiff.Strings(before, after)
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

var optimizeTierHeadings = []struct {
	tier  optimize.Tier
	title string
}{
	{optimize.TierSafe, "Safe rewrites"},
	{optimize.TierCaller, "Caller recommendations"},
	{optimize.TierTuple, "Tuple recommendations"},
}

// renderOptimize prints findings grouped by tier (report order kept within a
// tier) followed by the unified diff of the safe rewrite.
func renderOptimize(w io.Writer, findings []optimize.Finding, gated []optimize.GatedFinding, rw *rewriteOutput) error {
	first := true
	for _, g := range optimizeTierHeadings {
		var group []optimize.Finding
		for _, f := range findings {
			if f.Tier == g.tier {
				group = append(group, f)
			}
		}
		if len(group) == 0 {
			continue
		}
		if !first {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		first = false
		if _, err := fmt.Fprintf(w, "%s\n", style.Heading.Render(fmt.Sprintf("%s (%d)", g.title, len(group)))); err != nil {
			return err
		}
		for _, f := range group {
			if err := renderFinding(w, f); err != nil {
				return err
			}
		}
	}
	if rw != nil && rw.Diff != "" {
		if !first {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		first = false
		if _, err := fmt.Fprintf(w, "%s\n", style.Heading.Render("Diff")); err != nil {
			return err
		}
		for _, line := range strings.SplitAfter(rw.Diff, "\n") {
			if line == "" {
				continue
			}
			if _, err := io.WriteString(w, colorDiffLine(line)); err != nil {
				return err
			}
		}
	}
	if len(gated) > 0 {
		if !first {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s\n", style.Heading.Render(fmt.Sprintf("Not applied (%d)", len(gated)))); err != nil {
			return err
		}
		for _, g := range gated {
			if _, err := fmt.Fprintf(w, "  %s  %s — %s\n", style.Bold.Render(g.Type+"#"+g.Relation), style.Faint.Render("["+g.ID+"]"), g.Gate); err != nil {
				return err
			}
		}
	}
	return nil
}
func colorDiffLine(line string) string {
	body := strings.TrimSuffix(line, "\n")
	nl := line[len(body):]
	switch {
	case strings.HasPrefix(body, "+++"), strings.HasPrefix(body, "---"):
		return style.Bold.Render(body) + nl
	case strings.HasPrefix(body, "@@"):
		return style.Faint.Render(body) + nl
	case strings.HasPrefix(body, "+"):
		return style.Success.Render(body) + nl
	case strings.HasPrefix(body, "-"):
		return style.Failure.Render(body) + nl
	}
	return line
}

func renderFinding(w io.Writer, f optimize.Finding) error {
	head := fmt.Sprintf("  %s  %s", style.Bold.Render(f.Type+"#"+f.Relation), style.Faint.Render("["+f.ID+"]"))
	if f.Before != "" || f.After != "" {
		head += fmt.Sprintf("  %s %s %s", f.Before, style.IconArrow, f.After)
	}
	if _, err := fmt.Fprintln(w, head); err != nil {
		return err
	}
	if f.Reason != "" {
		if _, err := fmt.Fprintf(w, "    %s %s\n", style.Faint.Render("reason:"), f.Reason); err != nil {
			return err
		}
	}
	if f.Proof != "" {
		if _, err := fmt.Fprintf(w, "    %s %s\n", style.Faint.Render("proof:"), f.Proof); err != nil {
			return err
		}
	}
	if len(f.Weights) > 0 {
		if _, err := fmt.Fprintf(w, "    %s %s\n", style.Faint.Render("weights:"), formatWeights(f.Weights)); err != nil {
			return err
		}
	}
	return nil
}

// formatWeights renders user-type weights sorted by type; -1 is unbounded.
func formatWeights(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fmt.Sprint(m[k])
		if m[k] < 0 {
			v = "inf"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}
