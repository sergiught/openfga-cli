package playground

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/optimize/benchfmt"
	"github.com/sergiught/openfga-cli/internal/style"
)

// denyRows returns a profile's deny checks, most reads first, then by name.
func denyRows(r *bench.Result) []bench.CheckResult {
	if r == nil {
		return nil
	}
	var rows []bench.CheckResult
	for _, c := range r.Checks {
		if !c.Allowed {
			rows = append(rows, c)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Before.Reads != rows[j].Before.Reads {
			return rows[i].Before.Reads > rows[j].Before.Reads
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// optimizerBody renders the active optimizer view into a w×h pane.
func (m Model) optimizerBody(w, h int) string {
	if m.opt.view == optReview {
		return m.reviewBody(w, h)
	}
	var lines []string
	switch {
	case m.opt.running:
		lines = []string{m.spinner.View() + fmt.Sprintf(" benchmarking… (synthesized tuples, %d runs)", benchIterations)}
	case m.opt.err == noGraphMsg:
		lines = []string{style.Warn.Render(m.opt.err)}
	case m.opt.err != "":
		lines = []string{style.Failure.Render(m.opt.err)}
	case m.opt.profile != nil:
		lines = m.profileLines(w, h)
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}

func (m Model) profileLines(w, h int) []string {
	r := m.opt.profile
	head := []string{"Benchmark", r.Source, benchfmt.Plural(r.TupleCount, "tuple"), benchfmt.Plural(len(r.Checks), "check")}
	if r.Iterations > 0 {
		head = append(head, benchfmt.Plural(r.Iterations, "run"))
	}
	lines := []string{style.Heading.Render(strings.Join(head, " · ")), ""}
	lines = append(lines, benchfmt.Totals(r)...)
	lines = append(lines, "", style.Heading.Render("Most expensive deny checks"))

	foot := []string{style.Faint.Render("in-process, indicative only")}
	if m.opt.safeN > 0 {
		foot = append([]string{style.Success.Render("●") + fmt.Sprintf(" %s available — press o to review",
			benchfmt.Plural(m.opt.safeN, "safe rewrite"))}, foot...)
	}

	rows := denyRows(r)
	if len(rows) == 0 {
		return append(append(lines, style.Faint.Render("  no deny checks")), foot...)
	}
	names := make([]string, len(rows))
	nameW, maxReads := len("CHECK"), int64(1)
	for i, c := range rows {
		names[i] = strings.TrimPrefix(c.Name, "deny ")
		nameW = max(nameW, lipgloss.Width(names[i]))
		maxReads = max(maxReads, c.Before.Reads)
	}
	nameW = min(nameW, max(10, w/2-8))
	const numW = 6 + 2 + 10 + 2 + 7
	barW := max(0, w-1-nameW-2-numW-2)

	cell := func(name, reads, disp, p50 string) string {
		name = ansi.Truncate(name, nameW, "…")
		return fmt.Sprintf(" %s%s  %6s  %10s  %7s", name, strings.Repeat(" ", nameW-lipgloss.Width(name)), reads, disp, p50)
	}
	lines = append(lines, style.Faint.Render(cell("CHECK", "READS", "DISPATCHES", "P50 ms")))

	// Rows that fit between the header block and the footer, leaving a line
	// for "… N more".
	avail := max(1, h-len(lines)-len(foot)-1)
	cur := max(0, min(m.opt.cursor, len(rows)-1))
	start := max(0, cur-avail+1)
	end := min(len(rows), start+avail)
	for i := start; i < end; i++ {
		c := rows[i]
		l := cell(names[i], fmt.Sprint(c.Before.Reads), benchfmt.Dispatch(c.Before.Dispatches), fmt.Sprintf("%.2f", c.Before.P50Ms))
		if bar := int(float64(barW) * float64(c.Before.Reads) / float64(maxReads)); bar > 0 {
			l += "  " + style.Faint.Render(strings.Repeat("█", bar))
		}
		if i == cur {
			l = lipgloss.NewStyle().Reverse(true).Render(ansi.Strip(l))
		}
		lines = append(lines, l)
	}
	if more := len(rows) - (end - start); more > 0 {
		lines = append(lines, style.Faint.Render(fmt.Sprintf("  … %d more", more)))
	}
	return append(lines, foot...)
}

// clipLines fits s into at most h lines of at most w cells each.
func clipLines(s string, w, h int) string {
	lines := strings.Split(ansi.Hardwrap(s, max(1, w), true), "\n")
	if len(lines) > h {
		lines = lines[:max(0, h)]
	}
	return strings.Join(lines, "\n")
}

// reviewBody renders the review view: checklist of safe rewrites (with the
// advice below it) beside a detail card, or the selection benchmark.
func (m Model) reviewBody(w, h int) string {
	o := m.opt
	lw := w * 3 / 5 // wider than the default split: rows carry text
	cw := w - lw - 2
	cur := max(0, o.cursor)

	var lines []string
	curLine := 0
	applied := o.appliedKeys()
	switch {
	case len(o.rows) > 0:
		head := fmt.Sprintf("Safe rewrites · %d of %d accepted", len(o.accepted), len(o.rows))
		if n := len(applied); n != len(o.accepted) {
			head += fmt.Sprintf(" (%d apply)", n)
		}
		lines = append(lines, style.Heading.Render(head))
		for i, f := range o.rows {
			box := "[ ]"
			l := f.Type + "#" + f.Relation + " " + f.ID
			if o.accepted[o.keys[i]] {
				box = "[✓]"
				if !applied[o.keys[i]] {
					l += " depends on a rejected rewrite"
					lines = append(lines, o.reviewRow(box+" "+l, i == cur, true))
					if i == cur {
						curLine = len(lines) - 1
					}
					continue
				}
			}
			lines = append(lines, o.reviewRow(box+" "+l, i == cur, false))
			if i == cur {
				curLine = len(lines) - 1
			}
		}
	case o.report != nil && o.report.Blocked != "":
		lines = append(lines, strings.Split(ansi.Wrap(style.Warn.Render(o.report.Blocked), max(1, lw), ""), "\n")...)
	case o.report != nil:
		lines = append(lines, style.Faint.Render("Nothing to optimize"))
	}
	if len(o.advice) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, style.Heading.Render("Advice (not applied)"))
		for i, f := range o.advice {
			lines = append(lines, o.reviewRow(f.ID+" "+f.Type+"#"+f.Relation, len(o.rows)+i == cur, false))
			if len(o.rows)+i == cur {
				curLine = len(lines) - 1
			}
		}
	}
	// Window the list around the cursor.
	if len(lines) > h {
		start := max(0, min(curLine-h/2, len(lines)-h))
		lines = lines[start : start+h]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, lw, "…")
	}

	title, card := m.reviewCard(cur)
	title = ansi.Truncate(title, max(1, cw), "…")
	return masterDetailW(strings.Join(lines, "\n"), title, clipLines(card, cw, h-1), lw, w, h)
}

func (o optimizer) reviewRow(s string, selected, dim bool) string {
	switch {
	case selected:
		return lipgloss.NewStyle().Reverse(true).Render(s)
	case dim:
		return style.Faint.Render(s)
	}
	return s
}

// reviewCard returns the right pane's title and body for the row at cur.
func (m Model) reviewCard(cur int) (string, string) {
	o := m.opt
	switch {
	case o.running:
		return "", m.spinner.View() + fmt.Sprintf(" benchmarking selection… (%d runs)", benchIterations)
	case o.err == noGraphMsg:
		return "", style.Warn.Render(o.err)
	case o.err != "":
		return "", style.Failure.Render(o.err)
	case o.selBench != nil:
		var buf bytes.Buffer
		if err := benchfmt.Render(&buf, o.selBench); err != nil {
			return "", style.Failure.Render(errStr(err))
		}
		return "Selection benchmark", strings.TrimRight(buf.String(), "\n")
	}
	var f optimize.Finding
	switch {
	case cur < len(o.rows):
		f = o.rows[cur]
	case cur-len(o.rows) < len(o.advice):
		f = o.advice[cur-len(o.rows)]
	default:
		return "", ""
	}
	title := f.ID + " " + f.Type + "#" + f.Relation
	var b []string
	if cur < len(o.rows) {
		for _, l := range strings.Split(f.Before, "\n") {
			b = append(b, style.Failure.Render("- "+l))
		}
		for _, l := range strings.Split(f.After, "\n") {
			b = append(b, style.Success.Render("+ "+l))
		}
		b = append(b, "", f.Reason, "", style.Heading.Render("why it's safe"), f.Proof)
		return title, strings.Join(b, "\n")
	}
	b = append(b, f.Reason)
	if len(f.Weights) > 0 {
		keys := make([]string, 0, len(f.Weights))
		for k := range f.Weights {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			v := fmt.Sprint(f.Weights[k])
			if f.Weights[k] == -1 {
				v = "inf"
			}
			parts[i] = k + ": " + v
		}
		b = append(b, "", style.Heading.Render("weights"), strings.Join(parts, ", "))
	}
	return title, strings.Join(b, "\n")
}
