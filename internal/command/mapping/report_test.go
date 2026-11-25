package mapping

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func tup(user, rel, obj string) language.Tuple {
	return language.Tuple{User: user, Relation: rel, Object: obj}
}

func withCond(t language.Tuple, cond string, ctx map[string]any) language.Tuple {
	t.Condition, t.Context = cond, ctx
	return t
}

// reportRun is a run of the fake provider that read the types it lists.
func reportRun(desired int, counts ...snapshot.Count) *snapshotRun {
	res := fakeResult()
	res.Counts = counts
	return &snapshotRun{Provider: fakeProvider{}, Result: res, Desired: make([]language.Tuple, desired)}
}

// colorless renders with fn and strips the styling, as a pipe would.
func colorless(t *testing.T, fn func(w *bytes.Buffer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		t.Fatal(err)
	}
	return ansi.Strip(buf.String())
}

func setInteractive(t *testing.T, on bool) {
	t.Helper()
	prev := output.Interactive
	output.Interactive = on
	t.Cleanup(func() { output.Interactive = prev })
}

var storeAt = time.Date(2026, 11, 19, 14, 3, 13, 0, time.UTC)

func TestRenderDriftReportAllSections(t *testing.T) {
	setInteractive(t, false)
	run := reportRun(1500,
		snapshot.Count{Type: "snapshot.user", N: 1200},
		snapshot.Count{Type: "snapshot.organization.member", N: 7, Windowed: true})
	run.Result.RateLimited = 2
	run.Since = time.Date(2026, 11, 19, 12, 2, 13, 0, time.UTC)
	diff := snapshot.Diff{
		Missing: []language.Tuple{tup("user:anne", "member", "organization:acme"), tup("user:bob", "member", "organization:acme")},
		Changed: []language.Tuple{
			withCond(tup("user:anne", "viewer", "document:1"), "in_office", map[string]any{"ip": "10.0.0.1"}),
			tup("user:bob", "viewer", "document:2"),
		},
		Was: []language.Tuple{
			withCond(tup("user:anne", "viewer", "document:1"), "in_region", map[string]any{"region": "eu"}),
			withCond(tup("user:bob", "viewer", "document:2"), "in_region", nil),
		},
		Stale: []language.Tuple{tup("user:ghost", "assignee", "plan:pro"), tup("user:ghost", "member", "organization:acme")},
	}
	notChecked := []snapshot.NotChecked{{Rule: "user.updated", Reason: "no snapshot rule"}, {Rule: "a", Reason: "other"}}
	got := colorless(t, func(w *bytes.Buffer) error {
		return renderDriftReport(w, run, "demo", storeAt, 912, 0, false, diff, notChecked)
	})
	want := lines(
		"  Store  demo · 14:03:13 UTC · 912 tuples",
		"  Fake   tenant.example · 14:02:13 → 14:05:40 UTC · 2 rate-limit waits",
		"         snapshot.user                 1,200",
		"         snapshot.organization.member      7  (changed since 2026-11-19 12:02:13 UTC)",
		"",
		"  Drift  + 2 missing · ~ 2 changed · - 2 stale · 1,496 in sync",
		"",
		"  MISSING  2 tuples in Fake, not in the store",
		"  user member organization · 2",
		"    + user:anne member organization:acme",
		"    + user:bob member organization:acme",
		"",
		"  CHANGED  2 tuples whose condition differs",
		"  user viewer document · 2",
		`    ~ user:anne viewer document:1 (in_office: ip="10.0.0.1")  was (in_region: region="eu")`,
		"    ~ user:bob viewer document:2  was (in_region)",
		"",
		"  STALE  2 tuples in the store, not in Fake",
		"  user assignee plan · 1",
		"    - user:ghost assignee plan:pro",
		"  user member organization · 1",
		"    - user:ghost member organization:acme",
		"",
		"  NOT CHECKED  2 rules",
		"    user.updated  no snapshot rule",
		"    a             other",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderDriftReportSingularAndUnconditionalWas(t *testing.T) {
	setInteractive(t, false)
	run := reportRun(1, snapshot.Count{Type: "snapshot.user", N: 1})
	diff := snapshot.Diff{
		Changed: []language.Tuple{withCond(tup("user:a", "viewer", "document:1"), "c", nil)},
		Was:     []language.Tuple{tup("user:a", "viewer", "document:1")},
	}
	got := colorless(t, func(w *bytes.Buffer) error {
		return renderDriftReport(w, run, "demo", storeAt, 1, 0, false, diff, nil)
	})
	for _, want := range []string{"CHANGED  1 tuple whose condition differs", "~ user:a viewer document:1 (c)  was unconditional", "Drift  + 0 missing · ~ 1 changed · - 0 stale · 0 in sync"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestRenderDriftReportHeaderOrder(t *testing.T) {
	setInteractive(t, false)
	run := reportRun(0, snapshot.Count{Type: "snapshot.user", N: 3})
	render := func(after bool, at time.Time) string {
		return colorless(t, func(w *bytes.Buffer) error {
			return renderDriftReport(w, run, "demo", at, 5, 2, after, snapshot.Diff{}, nil)
		})
	}
	before := lines(
		"  Store  demo · 14:03:13 UTC · 5 tuples",
		"  Fake   tenant.example · 14:02:13 → 14:05:40 UTC",
		"         snapshot.user  3",
		"",
		"  Drift  + 0 missing · ~ 0 changed · - 0 stale · 0 in sync",
	)
	if got := render(false, storeAt); got != before {
		t.Errorf("store first:\n%s", got)
	}
	after := lines(
		"  Fake   tenant.example · 14:02:13 → 14:05:40 UTC",
		"         snapshot.user  3",
		"  Store  demo · 14:03:13 UTC · 5 tuples on 2 objects",
		"",
		"  Drift  + 0 missing · ~ 0 changed · - 0 stale · 0 in sync",
	)
	if got := render(true, storeAt); got != after {
		t.Errorf("store after the provider:\n%s", got)
	}
	none := lines(
		"  Fake   tenant.example · 14:02:13 → 14:05:40 UTC",
		"         snapshot.user  3",
		"",
		"  Drift  + 0 missing · ~ 0 changed · - 0 stale · 0 in sync",
	)
	if got := render(true, time.Time{}); got != none {
		t.Errorf("store not read:\n%s", got)
	}
}

func TestRenderBackfillReport(t *testing.T) {
	setInteractive(t, false)
	run := reportRun(4, snapshot.Count{Type: "snapshot.user", N: 4})
	diff := snapshot.Diff{
		Missing: []language.Tuple{tup("user:a", "member", "organization:o1")},
		Changed: []language.Tuple{withCond(tup("user:b", "member", "organization:o1"), "in_office", nil)},
		Was:     []language.Tuple{tup("user:b", "member", "organization:o1")},
	}
	got := colorless(t, func(w *bytes.Buffer) error {
		return renderBackfillReport(w, run, "demo", storeAt, 3, 0, diff, 2)
	})
	want := lines(
		"  Fake      tenant.example · 14:02:13 → 14:05:40 UTC",
		"            snapshot.user  4",
		"  Store     demo · 14:03:13 UTC · 3 tuples",
		"",
		"  Backfill  + 1 missing · ~ 1 with a different condition · 2 already there · 4 from Fake",
		"",
		"  MISSING  1 tuple in Fake, not in the store",
		"  user member organization · 1",
		"    + user:a member organization:o1",
		"",
		"  CHANGED  1 tuple whose condition differs",
		"  user member organization · 1",
		"    ~ user:b member organization:o1 (in_office)  was unconditional",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderBackfillReportNothingDesired(t *testing.T) {
	setInteractive(t, false)
	run := reportRun(0, snapshot.Count{Type: "snapshot.user", N: 0})
	got := colorless(t, func(w *bytes.Buffer) error {
		return renderBackfillReport(w, run, "demo", time.Time{}, 0, 0, snapshot.Diff{}, 0)
	})
	want := lines(
		"  Fake      tenant.example · 14:02:13 → 14:05:40 UTC",
		"            snapshot.user  0",
		"",
		"  Backfill  0 tuples from Fake",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// missingReport renders a drift report whose only difference is these tuples.
func missingReport(t *testing.T, ts []language.Tuple) string {
	t.Helper()
	setInteractive(t, false)
	return colorless(t, func(w *bytes.Buffer) error {
		return renderDriftReport(w, reportRun(len(ts)), "demo", storeAt, 0, 0, false, snapshot.Diff{Missing: ts}, nil)
	})
}

// section is the report from the first section title on.
func section(report string) string {
	_, s, _ := strings.Cut(report, "\n\n  MISSING")
	return "  MISSING" + s
}

func TestRenderReportCapsOneShape(t *testing.T) {
	var ts []language.Tuple
	for i := range 80 {
		ts = append(ts, tup(fmt.Sprintf("user:u%02d", i), "member", "organization:o"))
	}
	got := section(missingReport(t, ts))
	var want []string
	want = append(want, "  MISSING  80 tuples in Fake, not in the store", "  user member organization · 80")
	for i := range 50 {
		want = append(want, fmt.Sprintf("    + user:u%02d member organization:o", i))
	}
	want = append(want, "    … 30 more", "  -o json lists all 80")
	if got != lines(want...) {
		t.Errorf("got:\n%s", got)
	}
}

func TestRenderReportCapsEachShape(t *testing.T) {
	var ts []language.Tuple
	for i := range 40 {
		ts = append(ts, tup(fmt.Sprintf("user:a%02d", i), "member", "organization:o"))
	}
	for i := range 20 {
		ts = append(ts, tup(fmt.Sprintf("user:b%02d", i), "viewer", "document:d"))
	}
	got := section(missingReport(t, ts))
	// Two shapes share the 50 lines: 25 each.
	if n := strings.Count(got, "    + user:a"); n != 25 {
		t.Errorf("first shape shows %d tuples", n)
	}
	if n := strings.Count(got, "    + user:b"); n != 20 {
		t.Errorf("second shape shows %d tuples", n)
	}
	for _, want := range []string{"  user member organization · 40\n", "    … 15 more\n", "  user viewer document · 20\n", "  -o json lists all 60\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "more") != 1 {
		t.Errorf("only the first shape has hidden tuples:\n%s", got)
	}
}

func TestRenderReportShowsAllAtTheCap(t *testing.T) {
	var ts []language.Tuple
	for i := range maxPlanLines {
		ts = append(ts, tup(fmt.Sprintf("user:u%02d", i), "member", "organization:o"))
	}
	got := missingReport(t, ts)
	if strings.Count(got, "    + user:") != maxPlanLines || strings.Contains(got, "more") || strings.Contains(got, "-o json") {
		t.Errorf("a section of exactly %d tuples is shown whole:\n%s", maxPlanLines, got)
	}
}

func TestRenderReportBlankLinesOnlyOnATerminal(t *testing.T) {
	render := func() string {
		var buf bytes.Buffer
		if err := renderDriftReport(&buf, reportRun(0), "demo", storeAt, 0, 0, false, snapshot.Diff{}, nil); err != nil {
			t.Fatal(err)
		}
		return ansi.Strip(buf.String())
	}
	setInteractive(t, false)
	plain := render()
	if strings.HasPrefix(plain, "\n") || strings.HasSuffix(plain, "\n\n") {
		t.Errorf("redirected report has edge blanks: %q", plain)
	}
	setInteractive(t, true)
	if got := render(); got != "\n"+plain+"\n" {
		t.Errorf("terminal report = %q, want %q around", got, plain)
	}
}

func TestRenderShapeLines(t *testing.T) {
	ts := []language.Tuple{tup("user:a", "member", "organization:o"), tup("user:b", "member", "organization:o"), tup("user:a", "viewer", "document:d")}
	got := colorless(t, func(w *bytes.Buffer) error { return renderShapeLines(w, ts) })
	if want := lines("  STALE  3 tuples to delete", "  user member organization · 2", "  user viewer document · 1"); got != want {
		t.Errorf("got:\n%s", got)
	}
}
