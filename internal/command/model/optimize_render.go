package model

import (
	"fmt"
	"io"
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

// renderBench prints the benchmark table: deny rows first, then allow rows.
// The headline covers deny checks only; allow read totals are noise.
func renderBench(w io.Writer, r *bench.Result) error {
	if r == nil {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("Benchmark (%s, %s, %s)", r.Source, plural(r.TupleCount, "tuple"), plural(len(r.Checks), "check")))); err != nil {
		return err
	}
	profile := len(r.Checks) > 0 && r.Checks[0].After == nil
	cell := func(m *bench.Metrics) string {
		return fmt.Sprintf("%d / %.1f / %.2fms / %.2fms", m.Reads, m.Dispatches, m.P50Ms, m.P95Ms)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "CHECK\tANSWER\tBEFORE reads/dispatches/p50/p95"
	if !profile {
		head += "\tAFTER reads/dispatches/p50/p95"
	}
	_, _ = fmt.Fprintln(tw, head)
	for _, deny := range []bool{true, false} {
		for _, c := range r.Checks {
			if c.Allowed == deny {
				continue
			}
			ans := "allow"
			if deny {
				ans = "deny"
			}
			row := fmt.Sprintf("%s\t%s\t%s", c.Name, ans, cell(&c.Before))
			if c.After != nil {
				row += "\t" + cell(c.After)
			}
			_, _ = fmt.Fprintln(tw, row)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if err := renderAnswers(w, "Changed answers", "", r.Mismatches); err != nil {
		return err
	}
	if err := renderAnswers(w, "Unstable answers",
		"the original model answered these differently across re-runs, or they cannot reach a rewritten relation; not counted as changed", r.Unstable); err != nil {
		return err
	}
	t := r.Totals
	var line string
	if profile {
		line = fmt.Sprintf("deny reads %d, deny dispatches %.1f", t.DenyReadsBefore, t.DenyDispatchesBefore)
	} else {
		line = fmt.Sprintf("deny reads %d %s %d (deny dispatches %.1f %s %.1f)",
			t.DenyReadsBefore, style.IconArrow, t.DenyReadsAfter, t.DenyDispatchesBefore, style.IconArrow, t.DenyDispatchesAfter)
	}
	_, err := fmt.Fprintf(w, "%s\n%s\n", style.Bold.Render(line), style.Faint.Render("in-process, indicative only"))
	return err
}

// plural renders a count with its noun, adding an "s" unless the count is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
