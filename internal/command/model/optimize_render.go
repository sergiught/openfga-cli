package model

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/aymanbagabas/go-udiff"

	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/style"
)

// unifiedDiff diffs the normalized before/after DSL, labelled with the
// model file.
func unifiedDiff(file, before, after string) string {
	return udiff.Unified(file, file+" (optimized)", before, after)
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
func renderOptimize(w io.Writer, findings []optimize.Finding, rw *rewriteOutput) error {
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
	if rw == nil || rw.Diff == "" {
		return nil
	}
	if !first {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
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

// renderAnswers lists differing answers under a "<heading> (N)" heading, with
// an optional faint note; nothing when ms is empty.
func renderAnswers(w io.Writer, heading, note string, ms []bench.Mismatch) error {
	if len(ms) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("%s (%d)", heading, len(ms)))); err != nil {
		return err
	}
	if note != "" {
		if _, err := fmt.Fprintf(w, "%s\n", style.Faint.Render(note)); err != nil {
			return err
		}
	}
	answer := func(allowed bool) string {
		if allowed {
			return "allow"
		}
		return "deny"
	}
	for _, m := range ms {
		if _, err := fmt.Fprintf(w, "  %s: %s %s %s  %s %s %s\n", m.Name, m.User, m.Relation, m.Object,
			answer(m.AllowedBefore), style.IconArrow, answer(m.AllowedAfter)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// renderBench prints the benchmark report: deny-check totals, then the deny
// checks whose cost changed (or all of them in profile mode). Allow checks
// stop early, so their read counts are noise and only appear in the footer.
func renderBench(w io.Writer, r *bench.Result) error {
	if r == nil {
		return nil
	}
	profile := len(r.Checks) > 0 && r.Checks[0].After == nil
	head := []string{"Benchmark", r.Source, plural(r.TupleCount, "tuple"), plural(len(r.Checks), "check")}
	if r.Iterations > 0 {
		head = append(head, plural(r.Iterations, "run"))
	}
	if _, err := fmt.Fprintf(w, "\n%s\n\n", style.Heading.Render(strings.Join(head, " · "))); err != nil {
		return err
	}

	t := r.Totals
	dispatch := func(f float64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") }
	if profile {
		_, _ = fmt.Fprintf(w, "  Deny reads       %d\n  Deny dispatches  %s\n", t.DenyReadsBefore, dispatch(t.DenyDispatchesBefore))
	} else {
		_, _ = fmt.Fprintf(w, "  Deny reads       %s\n  Deny dispatches  %s\n",
			change(fmt.Sprint(t.DenyReadsBefore), fmt.Sprint(t.DenyReadsAfter), float64(t.DenyReadsBefore), float64(t.DenyReadsAfter)),
			change(dispatch(t.DenyDispatchesBefore), dispatch(t.DenyDispatchesAfter), t.DenyDispatchesBefore, t.DenyDispatchesAfter))
		answers := fmt.Sprintf("all %d unchanged", len(r.Checks))
		if len(r.Mismatches) > 0 {
			answers = style.Failure.Render(fmt.Sprintf("%d changed", len(r.Mismatches)))
		}
		if len(r.Unstable) > 0 {
			answers += fmt.Sprintf(" · %d unstable", len(r.Unstable))
		}
		_, _ = fmt.Fprintf(w, "  Answers          %s\n", answers)
	}

	var rows []bench.CheckResult
	unchanged, allow := 0, 0
	for _, c := range r.Checks {
		switch {
		case c.Allowed:
			allow++
		case profile || c.After.Reads != c.Before.Reads || c.After.Dispatches != c.Before.Dispatches:
			rows = append(rows, c)
		default:
			unchanged++
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if profile {
			if rows[i].Before.Reads != rows[j].Before.Reads {
				return rows[i].Before.Reads > rows[j].Before.Reads
			}
		} else if pi, pj := pct(rows[i].Before.Reads, rows[i].After.Reads), pct(rows[j].Before.Reads, rows[j].After.Reads); pi != pj {
			return pi < pj
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) > 0 {
		title := "Deny checks whose cost changed"
		if profile {
			title = "Deny checks"
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("%s (%d)", title, len(rows)))); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		if profile {
			_, _ = fmt.Fprintln(tw, "  CHECK\tREADS\tDISPATCHES\tP50 ms")
		} else {
			_, _ = fmt.Fprintln(tw, "  CHECK\tREADS\t\tDISPATCHES\t\tP50 ms")
		}
		for _, c := range rows {
			name := strings.TrimPrefix(c.Name, "deny ")
			if profile {
				_, _ = fmt.Fprintf(tw, "  %s\t%d\t%s\t%.2f\n", name, c.Before.Reads, dispatch(c.Before.Dispatches), c.Before.P50Ms)
				continue
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%d %s %d\t%s\t%s %s %s\t%s\t%.2f %s %.2f\n", name,
				c.Before.Reads, style.IconArrow, c.After.Reads, pctLabel(float64(c.Before.Reads), float64(c.After.Reads)),
				dispatch(c.Before.Dispatches), style.IconArrow, dispatch(c.After.Dispatches), pctLabel(c.Before.Dispatches, c.After.Dispatches),
				c.Before.P50Ms, style.IconArrow, c.After.P50Ms)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if err := renderAnswers(w, "Changed answers", "", r.Mismatches); err != nil {
		return err
	}
	if err := renderAnswers(w, "Unstable answers",
		"the original model answered these differently across re-runs, or they cannot reach a rewritten relation; not counted as changed", r.Unstable); err != nil {
		return err
	}

	var parts []string
	if unchanged > 0 {
		parts = append(parts, plural(unchanged, "deny check")+" unchanged")
	}
	if allow > 0 {
		parts = append(parts, plural(allow, "allow check")+" not compared")
	}
	if len(parts) > 0 {
		line := strings.Join(parts, " · ")
		if allow > 0 {
			line += " (allow checks stop early, so read counts are noise)"
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Faint.Render(line)); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s\n", style.Faint.Render("in-process, indicative only"))
	return err
}

// change renders "before → after -12%", omitting the percent when equal.
func change(before, after string, b, a float64) string {
	s := before + " " + style.IconArrow + " " + after
	if l := pctLabel(b, a); l != "" {
		s += " " + l
	}
	return s
}

// pct is the rounded percent change from b to a (0 when b is 0).
func pct(b, a int64) int {
	if b == 0 {
		return 0
	}
	return int(math.Round(float64(a-b) / float64(b) * 100))
}

// pctLabel is the signed rounded percent change, blank when the values are
// equal or the baseline is zero.
func pctLabel(b, a float64) string {
	if b == 0 || a == b {
		return ""
	}
	p := int(math.Round((a - b) / b * 100))
	if p == 0 {
		return "0%"
	}
	return fmt.Sprintf("%+d%%", p)
}

// plural renders a count with its noun, adding an "s" unless the count is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
