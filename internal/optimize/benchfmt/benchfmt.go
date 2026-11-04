// Package benchfmt renders a bench.Result as the human-readable benchmark
// report shared by the CLI and the playground.
package benchfmt

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/style"
)

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

// Render prints the benchmark report: deny-check totals, then the deny
// checks whose cost changed (or all of them in profile mode). Allow checks
// stop early, so their read counts are noise and only appear in the footer.
func Render(w io.Writer, r *bench.Result) error {
	if r == nil {
		return nil
	}
	profile := len(r.Checks) > 0 && r.Checks[0].After == nil
	if _, err := fmt.Fprintf(w, "\n%s\n\n", style.Heading.Render(strings.Join(header(r), " · "))); err != nil {
		return err
	}

	for _, l := range Totals(r) {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}

	if len(r.Skipped) > 0 {
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("Not synthesized (%d)", len(r.Skipped)))); err != nil {
			return err
		}
		for _, s := range r.Skipped {
			if _, err := fmt.Fprintf(w, "  %s\n", s); err != nil {
				return err
			}
		}
	}
	if len(r.Unexercised) > 0 {
		if _, err := fmt.Fprintf(w, "%s\n  %s\n", unexercisedWarning(r), style.Faint.Render(unexercisedHint)); err != nil {
			return err
		}
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
		// Each metric is a before/after pair; right-align both halves across
		// rows so the arrows line up.
		cells := make([][6]string, len(rows))
		var wd [6]int
		for i, c := range rows {
			cells[i] = [6]string{fmt.Sprint(c.Before.Reads), Dispatch(c.Before.Dispatches), fmt.Sprintf("%.2f", c.Before.P50Ms)}
			if !profile {
				cells[i][3], cells[i][4], cells[i][5] = fmt.Sprint(c.After.Reads), Dispatch(c.After.Dispatches), fmt.Sprintf("%.2f", c.After.P50Ms)
			}
			for k, v := range cells[i] {
				wd[k] = max(wd[k], len(v))
			}
		}
		for i, c := range rows {
			name := strings.TrimPrefix(c.Name, "deny ")
			v := cells[i]
			if profile {
				_, _ = fmt.Fprintf(tw, "  %s\t%*s\t%*s\t%*s\n", name, wd[0], v[0], wd[1], v[1], wd[2], v[2])
				continue
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", name,
				Pair(v[0], v[3], wd[0], wd[3]), PctLabel(float64(c.Before.Reads), float64(c.After.Reads)),
				Pair(v[1], v[4], wd[1], wd[4]), PctLabel(c.Before.Dispatches, c.After.Dispatches),
				Pair(v[2], v[5], wd[2], wd[5]))
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
		parts = append(parts, Plural(unchanged, "deny check")+" unchanged")
	}
	if allow > 0 {
		parts = append(parts, Plural(allow, "allow check")+" not compared")
	}
	if len(parts) > 0 {
		if _, err := fmt.Fprintf(w, "\n  %s\n", style.Faint.Render(strings.Join(parts, " · "))); err != nil {
			return err
		}
		if allow > 0 {
			if _, err := fmt.Fprintf(w, "  %s\n", style.Faint.Render("(allow checks stop early, so their read counts are noise)")); err != nil {
				return err
			}
		}
	} else if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  %s\n", style.Faint.Render("in-process, indicative only"))
	return err
}

const unexercisedHint = "benchmark with --workspace to cover them"

// unexercisedWarning is the warning line naming the rewritten relations the
// benchmark never exercised.
func unexercisedWarning(r *bench.Result) string {
	n := len(r.Unexercised)
	verb, noun, pron := "were", "rewritten relations", "them"
	if n == 1 {
		verb, noun, pron = "was", "rewritten relation", "it"
	}
	return style.Warn.Render(fmt.Sprintf("  %d %s %s only partly exercised (a skipped restriction reaches %s): %s", n, noun, verb, pron, strings.Join(r.Unexercised, ", ")))
}

// Unexercised returns the unexercised warning as lines, or nil when every
// rewritten relation was exercised.
func Unexercised(r *bench.Result) []string {
	if r == nil || len(r.Unexercised) == 0 {
		return nil
	}
	return []string{unexercisedWarning(r)}
}

// Summary returns the benchmark header, totals and unexercised warning as
// lines, without the table or any leading blank line.
func Summary(r *bench.Result) []string {
	if r == nil {
		return nil
	}
	lines := []string{style.Heading.Render(strings.Join(header(r), " · "))}
	lines = append(lines, Totals(r)...)
	return append(lines, Unexercised(r)...)
}

// header is the parts of the "Benchmark · ..." heading.
func header(r *bench.Result) []string {
	head := []string{"Benchmark", r.Source, Plural(r.TupleCount, "tuple"), Plural(len(r.Checks), "check")}
	if r.Iterations > 0 {
		head = append(head, Plural(r.Iterations, "run"))
	}
	return head
}

// Pair renders "before → after" with each half right-aligned to its width.
func Pair(before, after string, wb, wa int) string {
	return fmt.Sprintf("%*s %s %*s", wb, before, style.IconArrow, wa, after)
}

// change renders an aligned "before → after   -12%", omitting the percent
// when the values are equal.
func change(before, after string, wb, wa int, b, a float64) string {
	s := Pair(before, after, wb, wa)
	if l := PctLabel(b, a); l != "" {
		s += "   " + l
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

// PctLabel is the signed rounded percent change, blank when the values are
// equal or the baseline is zero.
func PctLabel(b, a float64) string {
	if b == 0 || a == b {
		return ""
	}
	p := int(math.Round((a - b) / b * 100))
	if p == 0 {
		return "0%"
	}
	return fmt.Sprintf("%+d%%", p)
}

// Plural renders a count with its noun, adding an "s" unless the count is 1.
func Plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Dispatch renders a dispatch count with at most one decimal.
func Dispatch(f float64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") }

// Totals returns the totals block lines exactly as Render prints them: deny
// reads and dispatches, plus the answers line unless r is a profile run.
func Totals(r *bench.Result) []string {
	t := r.Totals
	if len(r.Checks) > 0 && r.Checks[0].After == nil {
		return []string{
			fmt.Sprintf("  Deny reads       %d", t.DenyReadsBefore),
			fmt.Sprintf("  Deny dispatches  %s", Dispatch(t.DenyDispatchesBefore)),
		}
	}
	rb, ra := fmt.Sprint(t.DenyReadsBefore), fmt.Sprint(t.DenyReadsAfter)
	db, da := Dispatch(t.DenyDispatchesBefore), Dispatch(t.DenyDispatchesAfter)
	wb, wa := max(len(rb), len(db)), max(len(ra), len(da))
	answers := fmt.Sprintf("all %d unchanged", len(r.Checks))
	if len(r.Mismatches) > 0 {
		answers = style.Failure.Render(fmt.Sprintf("%d changed", len(r.Mismatches)))
	}
	if len(r.Unstable) > 0 {
		answers += fmt.Sprintf(" · %d unstable", len(r.Unstable))
	}
	return []string{
		"  Deny reads       " + change(rb, ra, wb, wa, float64(t.DenyReadsBefore), float64(t.DenyReadsAfter)),
		"  Deny dispatches  " + change(db, da, wb, wa, t.DenyDispatchesBefore, t.DenyDispatchesAfter),
		"  Answers          " + answers,
	}
}
