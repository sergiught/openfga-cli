package benchfmt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/optimize/bench"
)

func TestRenderBench_ListsChangedAnswers(t *testing.T) {
	r := &bench.Result{
		Source:     "synthesis",
		TupleCount: 1,
		Checks:     []bench.CheckResult{{Check: bench.Check{Name: "c"}, After: &bench.Metrics{}}},
		Mismatches: []bench.Mismatch{{
			Check:         bench.Check{Name: "probe 1", User: "user:a", Relation: "viewer", Object: "document:t1"},
			AllowedBefore: true,
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Benchmark · synthesis · 1 tuple · 1 check", "1 changed", "Changed answers (1)", "probe 1: user:a viewer document:t1  allow"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRenderBench_ChangedUnchangedAllowAndOrder(t *testing.T) {
	m := func(reads int64, disp, p50 float64) bench.Metrics {
		return bench.Metrics{Reads: reads, Dispatches: disp, P50Ms: p50}
	}
	after := func(x bench.Metrics) *bench.Metrics { return &x }
	chk := func(name string, allowed bool, b bench.Metrics, a *bench.Metrics) bench.CheckResult {
		return bench.CheckResult{Check: bench.Check{Name: name}, Allowed: allowed, Before: b, After: a}
	}
	r := &bench.Result{
		Source: "synthesis", TupleCount: 10, Iterations: 20,
		Checks: []bench.CheckResult{
			chk("deny worse", false, m(10, 2, 1), after(m(11, 2, 1))),
			chk("deny small", false, m(100, 5, 1), after(m(90, 5, 1))),
			chk("deny big", false, m(100, 4, 3.17), after(m(50, 2, 2.42))),
			chk("deny same", false, m(7, 1, 1), after(m(7, 1, 1))),
			chk("allow one", true, m(5, 1, 1), after(m(1, 1, 1))),
		},
		Totals: bench.Totals{DenyReadsBefore: 217, DenyReadsAfter: 158, DenyDispatchesBefore: 12, DenyDispatchesAfter: 9.5},
	}
	var buf bytes.Buffer
	if err := Render(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	wants := []string{
		"Benchmark · synthesis · 10 tuples · 5 checks · 20 runs",
		"Deny reads       217 → 158   -27%",
		"Deny dispatches   12 → 9.5   -21%",
		"all 5 unchanged",
		"Deny checks whose cost changed (3)",
		"big", "100 → 50", "-50%", "4 → 2", "-50%", "3.17 → 2.42",
		"small", "100 → 90", "-10%",
		"worse", " 10 → 11", "+10%",
		"1 deny check unchanged · 1 allow check not compared",
		"allow checks stop early",
		"in-process, indicative only",
	}
	pos := -1
	for _, w := range wants {
		i := strings.Index(out[pos+1:], w)
		if i < 0 {
			t.Fatalf("missing or out of order %q in:\n%s", w, out)
		}
		pos += 1 + i
	}
	for _, bad := range []string{"same", "allow one", "deny big"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, out)
		}
	}
}

func TestRenderBench_ProfileMode(t *testing.T) {
	r := &bench.Result{
		Source: "synthesis", TupleCount: 3,
		Checks: []bench.CheckResult{
			{Check: bench.Check{Name: "deny a"}, Before: bench.Metrics{Reads: 5, Dispatches: 1, P50Ms: 0.5}},
			{Check: bench.Check{Name: "deny b"}, Before: bench.Metrics{Reads: 9, Dispatches: 2.5, P50Ms: 1}},
			{Check: bench.Check{Name: "allow c"}, Allowed: true, Before: bench.Metrics{Reads: 1}},
		},
		Totals: bench.Totals{DenyReadsBefore: 14, DenyDispatchesBefore: 3.5},
	}
	var buf bytes.Buffer
	if err := Render(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Deny reads       14\n", "Deny dispatches  3.5\n", "Deny checks (2)", "1 allow check not compared"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Answers") || strings.Contains(out, "→") || strings.Contains(out, "unchanged") {
		t.Errorf("profile output has comparison content:\n%s", out)
	}
	if strings.Index(out, "\n  b ") > strings.Index(out, "\n  a ") {
		t.Errorf("rows not sorted by reads desc:\n%s", out)
	}
}

func TestTotals_MatchesRender(t *testing.T) {
	m := func(reads int64, disp float64) bench.Metrics { return bench.Metrics{Reads: reads, Dispatches: disp} }
	changed := &bench.Result{
		Source: "synthesis", TupleCount: 10,
		Checks: []bench.CheckResult{
			{Check: bench.Check{Name: "deny a"}, Before: m(100, 4), After: &bench.Metrics{Reads: 50, Dispatches: 2}},
		},
		Totals: bench.Totals{DenyReadsBefore: 217, DenyReadsAfter: 158, DenyDispatchesBefore: 12, DenyDispatchesAfter: 9.5},
	}
	profile := &bench.Result{
		Source: "synthesis", TupleCount: 3,
		Checks: []bench.CheckResult{{Check: bench.Check{Name: "deny a"}, Before: m(5, 1)}},
		Totals: bench.Totals{DenyReadsBefore: 14, DenyDispatchesBefore: 3.5},
	}
	for name, r := range map[string]*bench.Result{"changed": changed, "profile": profile} {
		var buf bytes.Buffer
		if err := Render(&buf, r); err != nil {
			t.Fatal(err)
		}
		out, lines := buf.String(), Totals(r)
		wantN := 3
		if name == "profile" {
			wantN = 2
		}
		if len(lines) != wantN {
			t.Errorf("%s: %d totals lines, want %d", name, len(lines), wantN)
		}
		pos := -1
		for _, l := range lines {
			i := strings.Index(out[pos+1:], l+"\n")
			if i < 0 {
				t.Fatalf("%s: totals line %q missing or out of order in:\n%s", name, l, out)
			}
			pos += 1 + i
		}
	}
}

func skippedResult(unexercised ...string) *bench.Result {
	return &bench.Result{
		Source: "synthesis", TupleCount: 3,
		Skipped:     []string{"doc#parent: conditioned-only restriction folder (condition active)"},
		Unexercised: unexercised,
		Checks:      []bench.CheckResult{{Check: bench.Check{Name: "deny a"}, Before: bench.Metrics{Reads: 5}, After: &bench.Metrics{Reads: 4}}},
		Totals:      bench.Totals{DenyReadsBefore: 5, DenyReadsAfter: 4},
	}
}

func TestRenderBench_ListsSkippedAndUnexercised(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, skippedResult("doc#viewer", "doc#can_view")); err != nil {
		t.Fatal(err)
	}
	out := ansi.Strip(buf.String())
	for _, want := range []string{
		"Not synthesized (1)",
		"\n  doc#parent: conditioned-only restriction folder (condition active)",
		"2 rewritten relations were only partly exercised (a skipped restriction reaches them): doc#viewer, doc#can_view",
		"benchmark with --workspace to cover them",
		"\n  in-process, indicative only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if ns, tbl := strings.Index(out, "Not synthesized"), strings.Index(out, "CHECK"); ns < 0 || tbl < 0 || ns > tbl {
		t.Errorf("skipped block should come before the table:\n%s", out)
	}
	buf.Reset()
	if err := Render(&buf, skippedResult("doc#viewer")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi.Strip(buf.String()), "1 rewritten relation was only partly exercised (a skipped restriction reaches it): doc#viewer") {
		t.Errorf("singular form missing:\n%s", buf.String())
	}
}

func TestSummary_HasNoTable(t *testing.T) {
	r := skippedResult("doc#viewer")
	lines := Summary(r)
	for i := range lines {
		lines[i] = ansi.Strip(lines[i])
	}
	out := strings.Join(lines, "\n")
	if !strings.HasPrefix(lines[0], "Benchmark ·") {
		t.Errorf("first line is not the header: %q", lines[0])
	}
	for _, l := range Totals(r) {
		if !strings.Contains(out, ansi.Strip(l)) {
			t.Errorf("summary lacks totals line %q:\n%s", l, out)
		}
	}
	if !strings.Contains(out, "1 rewritten relation was only partly exercised") {
		t.Errorf("summary lacks the unexercised warning:\n%s", out)
	}
	for _, l := range lines {
		if strings.Contains(l, "READS") || strings.Contains(l, "CHECK") {
			t.Errorf("summary has table header line %q", l)
		}
	}
	if strings.Contains(out, "Not synthesized") {
		t.Errorf("summary has skipped block:\n%s", out)
	}
}
