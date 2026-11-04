package model

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

var hunkHeader = regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@$`)

func TestUnifiedDiff_RoundTripsAndNumbersHunks(t *testing.T) {
	raw, err := os.ReadFile(optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	// Pad both sides with a distant, differing tail so the diff has a second
	// hunk (the reference's own edits all fall within one context window).
	pad := strings.Repeat("# pad\n", 20)
	before := string(raw) + pad + "# old tail\n"
	lm, err := modeltest.LoadModelBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := optimize.Analyze(lm.Proto)
	if err != nil || rep.Rewrite == nil {
		t.Fatalf("analyze: rewrite=%v err=%v", rep.Rewrite, err)
	}
	after, err := rep.Rewrite.After.DSL()
	if err != nil {
		t.Fatal(err)
	}

	after += pad + "# new tail\n"

	d := optimize.UnifiedDiff("reference.fga", before, after)
	got, err := applyUnified(d, before)
	if err != nil {
		t.Fatal(err)
	}
	if got != after {
		t.Fatalf("diff does not round-trip\n%s", d)
	}

	// Every hunk's +start must be its -start shifted by the net line change
	// of all earlier hunks; compute the expectation from the hunk bodies.
	hs := hunkHeader.FindAllStringSubmatch(d, -1)
	if len(hs) < 2 {
		t.Fatalf("want at least 2 hunks to exercise numbering, got %d\n%s", len(hs), d)
	}
	count := func(s string) int {
		if s == "" {
			return 1
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	delta := 0
	for i, h := range hs {
		oldStart, _ := strconv.Atoi(h[1])
		newStart, _ := strconv.Atoi(h[3])
		if newStart != oldStart+delta {
			t.Errorf("hunk %d: +%d, want +%d (delta %d)", i+1, newStart, oldStart+delta, delta)
		}
		delta += count(h[4]) - count(h[2])
	}
	if !strings.HasPrefix(d, "--- reference.fga\n+++ reference.fga (optimized)\n") {
		t.Errorf("unexpected labels: %q", d[:60])
	}
}

// applyUnified applies a unified diff with context lines. udiff.ApplyUnified
// only supports zero-context diffs, so it cannot check ours.
func applyUnified(diff, before string) (string, error) {
	src := strings.SplitAfter(before, "\n")
	var out strings.Builder
	pos := 0
	for _, l := range strings.SplitAfter(diff, "\n") {
		switch {
		case l == "", strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "\\"):
		case strings.HasPrefix(l, "@@"):
			m := hunkHeader.FindStringSubmatch(strings.TrimSuffix(l, "\n"))
			if m == nil {
				return "", fmt.Errorf("bad header %q", l)
			}
			start, _ := strconv.Atoi(m[1])
			for ; pos < start-1; pos++ {
				out.WriteString(src[pos])
			}
		case l[0] == '-' || l[0] == ' ':
			if pos >= len(src) || src[pos] != l[1:] {
				return "", fmt.Errorf("line %d: diff has %q, file has %q", pos+1, l[1:], src[min(pos, len(src)-1)])
			}
			if l[0] == ' ' {
				out.WriteString(src[pos])
			}
			pos++
		case l[0] == '+':
			out.WriteString(l[1:])
		default:
			return "", fmt.Errorf("bad line %q", l)
		}
	}
	for ; pos < len(src); pos++ {
		out.WriteString(src[pos])
	}
	return out.String(), nil
}
