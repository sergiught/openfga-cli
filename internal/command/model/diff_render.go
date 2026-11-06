package model

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/style"
)

// renderDiff prints the human report: the two sides, then the non-empty
// sections in severity order, then a one-line summary.
func renderDiff(w io.Writer, out diffOutput) error {
	c := style.SanitizeTerminal
	if _, err := fmt.Fprintf(w, "%s %s\n%s %s\n", style.Faint.Render("old:"), c(out.Old), style.Faint.Render("new:"), c(out.New)); err != nil {
		return err
	}
	sections := []struct {
		title   string
		entries []modeldiff.Entry
	}{{"Breaking", out.Breaking}, {"Changed", out.Changed}, {"Added", out.Added}}
	for _, s := range sections {
		if len(s.entries) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("%s (%d)", s.title, len(s.entries)))); err != nil {
			return err
		}
		for _, e := range s.entries {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", style.Bold.Render(c(entrySubject(e))), c(e.Detail)); err != nil {
				return err
			}
		}
	}
	if len(out.Cost) > 0 {
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(fmt.Sprintf("Cost (%d)", len(out.Cost)))); err != nil {
			return err
		}
		for _, ce := range out.Cost {
			if _, err := fmt.Fprintf(w, "  %s: %s (%s)\n", style.Bold.Render(c(ce.Type+"#"+ce.Relation)), ce.Direction, c(formatCostChange(ce))); err != nil {
				return err
			}
		}
	}
	line := "no changes"
	if len(out.Breaking)+len(out.Changed)+len(out.Added)+len(out.Cost) > 0 {
		line = diffSummaryText(out.Summary)
	}
	_, err := fmt.Fprintf(w, "\n%s\n", line)
	return err
}

func entrySubject(e modeldiff.Entry) string {
	switch {
	case e.Condition != "":
		return "condition " + e.Condition
	case e.Kind == modeldiff.SchemaVersionChanged:
		return "schema"
	case e.Relation != "":
		return e.Type + "#" + e.Relation
	}
	return e.Type
}

func formatCostChange(ce modeldiff.CostEntry) string {
	seen := map[string]bool{}
	var users []string
	for _, m := range []map[string]int{ce.Old, ce.New} {
		for u := range m {
			if !seen[u] {
				seen[u] = true
				users = append(users, u)
			}
		}
	}
	sort.Strings(users)
	parts := make([]string, 0, len(users))
	for _, u := range users {
		parts = append(parts, fmt.Sprintf("%s %s → %s", u, weightText(ce.Old[u]), weightText(ce.New[u])))
	}
	return strings.Join(parts, ", ")
}

func weightText(w int) string {
	if w < 0 {
		return "inf"
	}
	return fmt.Sprint(w)
}

// diffSummaryText is the one-line count summary, e.g. "2 breaking, 1 changed,
// 1 cost increase". Zero counts are left out.
func diffSummaryText(s diffSummary) string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(s.Breaking, "breaking")
	add(s.Changed, "changed")
	add(s.Added, "added")
	add(s.CostIncreased, "cost increase")
	add(s.CostDecreased, "cost decrease")
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}
