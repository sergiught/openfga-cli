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

	// Rows that fit between the header block and the footer; when they do not
	// all fit, two lines are kept for "… N above" / "… N below".
	avail := max(1, h-len(lines)-len(foot))
	if len(rows) > avail {
		avail = max(1, avail-2)
	}
	cur := max(0, min(m.opt.cursor, len(rows)-1))
	start := max(0, min(cur-avail/2, len(rows)-avail))
	end := min(len(rows), start+avail)
	if start > 0 {
		lines = append(lines, style.Faint.Render(fmt.Sprintf("  … %d above", start)))
	}
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
	if below := len(rows) - end; below > 0 {
		lines = append(lines, style.Faint.Render(fmt.Sprintf("  … %d below", below)))
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
			if o.accepted[o.keys[i]] {
				box = "[✓]"
			}
			l := box + " " + f.Type + "#" + f.Relation + " " + f.ID
			dep := o.accepted[o.keys[i]] && !applied[o.keys[i]]
			if dep {
				const suffix = " depends on a rejected rewrite"
				l = ansi.Truncate(l, max(0, lw-lipgloss.Width(suffix)), "…") + suffix
			}
			lines = append(lines, o.reviewRow(l, i == cur, dep))
			if i == cur {
				curLine = len(lines) - 1
			}
		}
	case o.report != nil && o.report.Blocked != "":
		lines = append(lines, strings.Split(ansi.Wrap(style.Warn.Render(o.report.Blocked), max(1, lw), ""), "\n")...)
	case o.report != nil && len(o.advice) == 0 && len(o.gated) == 0:
		lines = append(lines, style.Faint.Render("Nothing to optimize"))
	case o.report != nil && len(o.gated) == 0:
		lines = append(lines, wrapLines(style.Faint.Render("No safe rewrites — nothing to change automatically"), lw)...)
	}
	if len(o.gated) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, style.Heading.Render(fmt.Sprintf("Derived, not applied · %d", len(o.gated))))
		for i, g := range o.gated {
			sel := len(o.rows)+i == cur
			lines = append(lines, o.reviewRow("    "+g.Type+"#"+g.Relation+" "+g.ID+" (not applied)", sel, true))
			if sel {
				curLine = len(lines) - 1
			}
		}
	}
	adv := len(o.rows) + len(o.gated)
	if len(o.advice) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, style.Heading.Render(fmt.Sprintf("Worth a look · %d", len(o.advice))))
		lines = append(lines, wrapLines(style.Faint.Render("can't be rewritten safely — select one for details"), lw)...)
		for i, f := range o.advice {
			lines = append(lines, o.reviewRow(f.ID+" "+f.Type+"#"+f.Relation, adv+i == cur, false))
			if adv+i == cur {
				curLine = len(lines) - 1
			}
		}
	}
	// Window the list around the cursor.
	if h < 1 {
		lines = nil
	} else if len(lines) > h {
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
	case selected && dim:
		return lipgloss.NewStyle().Reverse(true).Faint(true).Render(s)
	case selected:
		return lipgloss.NewStyle().Reverse(true).Render(s)
	case dim:
		return style.Faint.Render(s)
	}
	return s
}

// wrapLines wraps s to width w and splits it into lines.
func wrapLines(s string, w int) []string {
	return strings.Split(ansi.Wrap(s, max(1, w), ""), "\n")
}

// adviceNames are the short names shown in an advice card's title.
var adviceNames = map[string]string{
	"C1": "recursion under and/but not",
	"C2": "exclusion",
	"T1": "recursive relation",
	"T2": "wide union",
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
	adv := len(o.rows) + len(o.gated)
	gated := cur >= len(o.rows) && cur < adv
	switch {
	case cur < len(o.rows):
		f = o.rows[cur]
	case gated:
		f = o.gated[cur-len(o.rows)].Finding
	case cur-adv < len(o.advice):
		f = o.advice[cur-adv]
	default:
		return "", ""
	}
	title := f.ID + " " + f.Type + "#" + f.Relation
	if gated {
		title += " (not applied)"
		return title, strings.Join([]string{
			style.Warn.Render("Not applied: " + o.gated[cur-len(o.rows)].Gate), "",
			style.Failure.Render("- " + f.Before), style.Success.Render("+ " + f.After), "", f.Reason,
		}, "\n")
	}
	if cur >= len(o.rows) {
		title = f.Type + "#" + f.Relation + " · " + f.ID
		if n := adviceNames[f.ID]; n != "" {
			title += " · " + n
		}
	}
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
		b = append(b, "", style.Heading.Render("cost per user type (inf = grows with your data)"), strings.Join(parts, ", "))
	}
	return title, strings.Join(b, "\n")
}

// writeDialogLayout builds the write dialog's fixed lines above the diff and
// its footer, plus how many diff lines fit between them.
func (m Model) writeDialogLayout() (head []string, foot string, diffH int) {
	o := m.opt
	w, h := m.sh.DialogSize()
	store := m.currentStoreName()
	if store == "" {
		store = m.storeID
	}
	n := len(o.subset.Applied)
	head = []string{style.Value.Render(fmt.Sprintf("%s · new model version on %s", benchfmt.Plural(n, "rewrite"), safeText(store))), ""}
	wrap := func(s string) {
		head = append(head, strings.Split(ansi.Wrap(s, max(1, w), ""), "\n")...)
	}
	switch {
	case o.running:
		head = append(head, m.spinner.View()+" checking answers…")
	case o.err != "":
		wrap(style.Failure.Render(o.err))
	case o.writeNoGraph:
		wrap(style.Warn.Render(noGraphMsg + " — rewrites are proven safe by the rules but not double-checked against answers"))
	case o.writeBench != nil && len(o.writeBench.Mismatches) > 0:
		ms := o.writeBench.Mismatches
		head = append(head, style.Failure.Render(benchfmt.Plural(len(ms), "answer")+" changed — not writing"))
		for _, mm := range ms[:min(5, len(ms))] {
			head = append(head, ansi.Truncate(fmt.Sprintf("  %s: %s %s %s", mm.Name, mm.User, mm.Relation, mm.Object), w, "…"))
		}
		head = append(head, style.Faint.Render("please report this as a bug"))
	case o.writeBench != nil:
		head = append(head, benchfmt.Totals(o.writeBench)...)
	}
	if o.writeBench != nil && len(o.writeBench.Unstable) > 0 && !o.running {
		head = append(head, style.Faint.Render(benchfmt.Plural(len(o.writeBench.Unstable), "unstable answer")+" differ between runs (engine nondeterminism, not blocking)"))
	}
	if o.writeErr != "" {
		wrap(style.Failure.Render("write failed: " + o.writeErr))
	}
	foot = style.Faint.Render("esc cancel")
	if o.canWrite() {
		foot = style.Faint.Render("y write · esc cancel")
	}
	return head, foot, max(0, h-len(head)-2)
}

// writeDialogBody renders the write dialog.
func (m Model) writeDialogBody() string {
	w, h := m.sh.DialogSize()
	head, foot, diffH := m.writeDialogLayout()
	lines := append([]string{}, head...)
	lines = append(lines, "")
	d := m.opt.dialogDiff
	start := max(0, min(m.opt.dialogScroll, len(d)-diffH))
	for _, l := range d[start:min(len(d), start+diffH)] {
		switch {
		case strings.HasPrefix(l, "+"):
			l = style.Success.Render(l)
		case strings.HasPrefix(l, "-"):
			l = style.Failure.Render(l)
		case strings.HasPrefix(l, "@@"):
			l = style.Faint.Render(l)
		}
		lines = append(lines, l)
	}
	if len(lines) > h-1 {
		lines = lines[:max(0, h-1)]
	}
	lines = append(lines, foot)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}
