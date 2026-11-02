package playground

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
