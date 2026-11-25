package mapping

import (
	"fmt"
	"image/color"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

// The human report of drift and backfill, as printed when not --plain:
// header rows (store, provider), a summary row, then one section per kind of
// difference with its tuples grouped by shape. Every color comes from
// internal/style, and the layout reads the same once escapes are stripped.

// headerRow is one labelled row of the report header; extra lines hang
// under it, aligned with its text.
type headerRow struct {
	label string
	text  string
	extra []string
}

// reportSection is one kind of difference: a chip, what it says, and tuples.
// was holds the stored version of each tuple (changed sections only).
type reportSection struct {
	chip   string
	color  color.Color
	sign   string
	noun   string // text after the count, e.g. "in Auth0, not in the store"
	tuples []language.Tuple
	was    []language.Tuple
}

// renderDriftReport prints the drift report. The store row comes before the
// provider rows unless it was read after them, and is left out when the store
// was not read at all.
func renderDriftReport(w io.Writer, run *snapshotRun, storeLabel string, storeReadAt time.Time, stored, objects int, afterProvider bool, diff snapshot.Diff, notChecked []snapshot.NotChecked) error {
	label := output.SanitizeField(run.Provider.Label())
	var rows []headerRow
	if !afterProvider {
		rows = append(rows, storeRow(run, storeLabel, storeReadAt, stored, 0))
	}
	rows = append(rows, providerRow(label, run.Result, run.Since))
	if afterProvider && !storeReadAt.IsZero() {
		rows = append(rows, storeRow(run, storeLabel, storeReadAt, stored, objects))
	}

	missing, changed := len(diff.Missing), len(diff.Changed)
	sync := len(run.Desired) - missing - changed
	summary := strings.Join([]string{
		countPart("+", missing, "missing", style.Green),
		countPart("~", changed, "changed", style.Amber),
		countPart("-", len(diff.Stale), "stale", style.Red),
		muted(thousands(sync) + " in sync"),
	}, joint())
	rows = append(rows, headerRow{label: "Drift", text: summary})

	return writeReport(w, rows, []reportSection{
		{"MISSING", style.Green, "+", "in " + label + ", not in the store", diff.Missing, nil},
		{"CHANGED", style.Amber, "~", "whose condition differs", diff.Changed, diff.Was},
		{"STALE", style.Red, "-", "in the store, not in " + label, diff.Stale, nil},
	}, notChecked)
}

// renderBackfillReport prints the backfill report. With nothing desired it is
// the provider row and a line saying so; the store was not read.
func renderBackfillReport(w io.Writer, run *snapshotRun, storeLabel string, storeReadAt time.Time, stored, objects int, diff snapshot.Diff, existing int) error {
	label := output.SanitizeField(run.Provider.Label())
	rows := []headerRow{providerRow(label, run.Result, run.Since)}
	if len(run.Desired) == 0 {
		rows = append(rows, headerRow{label: "Backfill", text: style.Bold.Render(countNoun(0, "tuple")) + muted(" from "+label)})
		return writeReport(w, rows, nil, nil)
	}
	rows = append(rows, storeRow(run, storeLabel, storeReadAt, stored, objects))
	parts := []string{countPart("+", len(diff.Missing), "missing", style.Green)}
	if n := len(diff.Changed); n > 0 {
		parts = append(parts, countPart("~", n, "with a different condition", style.Amber))
	}
	parts = append(parts,
		muted(thousands(existing)+" already there"),
		muted(thousands(len(run.Desired))+" from "+label))
	rows = append(rows, headerRow{label: "Backfill", text: strings.Join(parts, joint())})

	return writeReport(w, rows, []reportSection{
		{"MISSING", style.Green, "+", "in " + label + ", not in the store", diff.Missing, nil},
		{"CHANGED", style.Amber, "~", "whose condition differs", diff.Changed, diff.Was},
	}, nil)
}

// writeReport lays out the header rows, then the sections that have tuples,
// then the rules that were not checked, and writes them in one go. A blank
// line sets the report off from the lines around it on a terminal.
func writeReport(w io.Writer, rows []headerRow, sections []reportSection, notChecked []snapshot.NotChecked) error {
	var b strings.Builder
	if output.Interactive {
		b.WriteString("\n")
	}

	width := 0
	for _, r := range rows {
		width = max(width, lipgloss.Width(r.label))
	}
	indent := strings.Repeat(" ", 2+width+2)
	for i, r := range rows {
		if i == len(rows)-1 {
			b.WriteString("\n") // the summary stands apart from the sources
		}
		b.WriteString("  " + style.Bold.Render(r.label) + strings.Repeat(" ", width-lipgloss.Width(r.label)) + "  " + r.text + "\n")
		for _, e := range r.extra {
			b.WriteString(indent + e + "\n")
		}
	}

	for _, s := range sections {
		if len(s.tuples) > 0 {
			b.WriteString("\n")
			writeSection(&b, s)
		}
	}
	if len(notChecked) > 0 {
		b.WriteString("\n")
		writeNotChecked(&b, notChecked)
	}

	if output.Interactive {
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// storeRow says when the store was read and how many tuples it held. A
// nonzero objects means only those objects were read, so the count is of the
// tuples they held.
func storeRow(run *snapshotRun, storeLabel string, storeReadAt time.Time, stored, objects int) headerRow {
	layout := snapshotLayout(run.Result)
	if storeReadAt.UTC().Format(time.DateOnly) != run.Result.FinishedAt.UTC().Format(time.DateOnly) {
		layout = dateClockLayout // the store was read on another day than the provider finished
	}
	count := countNoun(stored, "tuple")
	if objects > 0 {
		count += " on " + countNoun(objects, "object")
	}
	return headerRow{label: "Store", text: strings.Join([]string{
		fg(output.SanitizeField(storeLabel)),
		muted(storeReadAt.UTC().Format(layout) + " UTC"),
		fg(count),
	}, joint())}
}

// providerRow says where and when the provider was read, with a line under it
// for each event type it produced. A count read by window says since when.
func providerRow(label string, r *snapshot.Result, since time.Time) headerRow {
	start, finish := snapshotTimes(r)
	parts := []string{
		fg(output.SanitizeField(r.Source)),
		muted(start) + " " + faint("→") + " " + muted(finish+" UTC"),
	}
	if r.RateLimited > 0 {
		parts = append(parts, style.Warn.Render(mapping.Plural(r.RateLimited, "rate-limit wait")))
	}

	typeWidth, countWidth := 0, 0
	types, counts := make([]string, len(r.Counts)), make([]string, len(r.Counts))
	for i, c := range r.Counts {
		types[i], counts[i] = output.SanitizeField(c.Type), thousands(c.N)
		typeWidth = max(typeWidth, lipgloss.Width(types[i]))
		countWidth = max(countWidth, len(counts[i]))
	}
	var extra []string
	for i, c := range r.Counts {
		line := muted(types[i]) + strings.Repeat(" ", typeWidth-lipgloss.Width(types[i])+2+countWidth-len(counts[i])) + fg(counts[i])
		if c.Windowed {
			line += faint("  (changed since " + since.UTC().Format(dateClockLayout) + " UTC)")
		}
		extra = append(extra, line)
	}
	return headerRow{label: label, text: strings.Join(parts, joint()), extra: extra}
}

// writeSection prints a section's title and its tuples grouped by shape. A
// long section shows an even share of its tuples for each shape.
func writeSection(b *strings.Builder, s reportSection) {
	n := len(s.tuples)
	b.WriteString(" " + style.Chip(s.chip, style.OnAccent, s.color) + " " + fg(countNoun(n, "tuple")) + muted(" "+s.noun) + "\n")

	groups := groupByShape(s.tuples)
	limit := n
	if n > maxPlanLines {
		limit = max(1, maxPlanLines/len(groups))
	}
	sign := lipgloss.NewStyle().Bold(true).Foreground(s.color).Render(s.sign)
	for _, g := range groups {
		b.WriteString("  " + shapeHeading(g.shape, len(g.idx)) + "\n")
		for _, i := range g.idx[:min(len(g.idx), limit)] {
			line := "    " + sign + " " + tupleBody(s.tuples[i])
			if s.was != nil {
				line += "  " + faint("was "+wasText(s.was[i]))
			}
			b.WriteString(line + "\n")
		}
		if hidden := len(g.idx) - limit; hidden > 0 {
			b.WriteString("    " + faint(fmt.Sprintf("… %s more", thousands(hidden))) + "\n")
		}
	}
	if n > maxPlanLines {
		b.WriteString("  " + faint("-o json lists all "+thousands(n)) + "\n")
	}
}

// writeNotChecked prints the rules whose changes the snapshot cannot see.
func writeNotChecked(b *strings.Builder, notChecked []snapshot.NotChecked) {
	b.WriteString(" " + style.Chip("NOT CHECKED", style.Fg, style.BgHighlight) + " " + fg(mapping.Plural(len(notChecked), "rule")) + "\n")
	width := 0
	for _, n := range notChecked {
		width = max(width, lipgloss.Width(output.SanitizeField(n.Rule)))
	}
	for _, n := range notChecked {
		rule := output.SanitizeField(n.Rule)
		b.WriteString("    " + fg(rule) + strings.Repeat(" ", width-lipgloss.Width(rule)) + "  " + muted(output.SanitizeField(n.Reason)) + "\n")
	}
}

// shapeGroup is the tuples of one shape, by index, in first-seen order.
type shapeGroup struct {
	shape string
	idx   []int
}

func groupByShape(ts []language.Tuple) []shapeGroup {
	var groups []shapeGroup
	at := map[mapping.Shape]int{}
	for i, t := range ts {
		s := mapping.ShapeOf(t)
		g, ok := at[s]
		if !ok {
			g = len(groups)
			at[s] = g
			groups = append(groups, shapeGroup{shape: output.SanitizeField(s.String())})
		}
		groups[g].idx = append(groups[g].idx, i)
	}
	return groups
}

// renderShapeLines prints what the prune question is about: how many stale
// tuples there are, and how many of each shape.
func renderShapeLines(w io.Writer, ts []language.Tuple) error {
	var b strings.Builder
	b.WriteString(" " + style.Chip("STALE", style.OnAccent, style.Red) + " " + fg(countNoun(len(ts), "tuple")) + muted(" to delete") + "\n")
	for _, g := range groupByShape(ts) {
		b.WriteString("  " + shapeHeading(g.shape, len(g.idx)) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func shapeHeading(shape string, n int) string {
	return style.Bold.Render(shape) + " " + faint("· "+thousands(n))
}

// tupleBody is a tuple without its sign: type prefixes muted, ids plain, the
// relation in the accent color, and the condition faint.
func tupleBody(t language.Tuple) string {
	head, cond := tupleParts(t)
	body := typed(head[0]) + " " + style.Key.Render(head[1]) + " " + typed(head[2])
	if cond != "" {
		body += faint(cond)
	}
	return body
}

// wasText says what the store holds instead: its condition, or none.
func wasText(t language.Tuple) string {
	if t.Condition == "" {
		return "unconditional"
	}
	_, cond := tupleParts(t)
	return strings.TrimSpace(cond)
}

// tupleParts splits a tuple into its user, relation and object, and the
// condition suffix (" (cond: k=v)", or empty), all sanitized.
func tupleParts(t language.Tuple) (head [3]string, cond string) {
	head = [3]string{output.SanitizeField(t.User), output.SanitizeField(t.Relation), output.SanitizeField(t.Object)}
	// ChangeLine opens with a one-character sign and a space, whatever the action.
	line := mapping.ChangeLine(t, output.SanitizeField)
	return head, strings.TrimPrefix(line[2:], strings.Join(head[:], " "))
}

// typed renders "type:id" with the type muted and the id plain.
func typed(s string) string {
	if t, id, ok := strings.Cut(s, ":"); ok {
		return muted(t+":") + fg(id)
	}
	return fg(s)
}

// countPart is "+ 5 missing": bold in color when n is above zero, faint at zero.
func countPart(sign string, n int, what string, c color.Color) string {
	st := lipgloss.NewStyle().Bold(true).Foreground(c)
	if n == 0 {
		st = style.Faint
	}
	return st.Render(sign + " " + thousands(n) + " " + what)
}

// countNoun is "1,200 tuples".
func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return thousands(n) + " " + noun + "s"
}

func joint() string         { return " " + faint("·") + " " }
func fg(s string) string    { return lipgloss.NewStyle().Foreground(style.Fg).Render(s) }
func muted(s string) string { return lipgloss.NewStyle().Foreground(style.Muted).Render(s) }
func faint(s string) string { return style.Faint.Render(s) }

// bold is the leading quantity of a result line.
func bold(s string) string { return style.Bold.Render(s) }
