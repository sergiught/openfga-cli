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
