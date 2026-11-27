package mapping

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

// plainReport makes the commands render for --plain, restoring the global after.
func plainReport(t *testing.T) {
	t.Helper()
	prev := output.Plain
	output.Plain = true
	t.Cleanup(func() { output.Plain = prev })
}

// conditionedMember is a stored member tuple carrying a condition.
func conditionedMember(user, cond string) map[string]any {
	m := member(user)
	m["condition"] = map[string]any{"name": cond}
	return m
}

// driftReportMapping is driftMapping with a condition on the member tuple.
var driftReportMapping = strings.Replace(driftMapping,
	"        object: \"organization:{{ input.org }}\"\n  - name: organization.member.removed",
	"        object: \"organization:{{ input.org }}\"\n        condition: in_office\n  - name: organization.member.removed", 1)

func TestDriftPlainReportGolden(t *testing.T) {
	configtest.Isolate(t)
	plainReport(t)
	pinNow(t, 3*time.Minute)
	fs := newFakeStore(t, conditionedMember("u1", "in_region"), member("u3"), member("gone"), identityTuple())
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2", "u3")})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftReportMapping, "--dry-run")...)
	if err == nil {
		t.Fatalf("expected drift exit\n%s", stderr)
	}
	if stdout != driftPlainGolden {
		t.Errorf("stdout:\n%q", stdout)
	}
}

func TestBackfillPlainReportGolden(t *testing.T) {
	configtest.Isolate(t)
	plainReport(t)
	pinNow(t, 3*time.Minute)
	fs := newFakeStore(t, conditionedMember("u1", "in_region"), member("u3"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2", "u3")})
	mp := writeFile(t, t.TempDir(), "m.yaml", driftReportMapping)
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if stdout != backfillPlainGolden {
		t.Errorf("stdout:\n%q", stdout)
	}
}

const driftPlainGolden = "store read  14:05:13 UTC  4 tuples  acme\nFake read  14:02:13 → 14:05:40 UTC  tenant.example\n  snapshot.organization.member  2\n\nmissing  in Fake, not in the store\n1 tuple\nuser member organization\t1\n+ user:u2 member organization:o1 (in_office)\n\nchanged  condition differs\n2 tuples\nuser member organization\t2\n+ user:u1 member organization:o1 (in_office) was (in_region)\n+ user:u3 member organization:o1 (in_office) was no condition\n\nstale    in the store, not in Fake\n1 tuple\nuser member organization\t1\n- user:gone member organization:o1\n\nnot checked\n  user.updated  no snapshot rule writes user identity connection\nstore\t01ARZ3NDEKTSV4RRFFQ69G5FAV\nprovider\tfake\ndry_run\ttrue\nmissing\t1\nchanged\t2\nstale\t1\nnot_checked\t1\npruned\t0\nwrites\t0\ndeletes\t0\n"
const backfillPlainGolden = "Fake read  14:02:13 → 14:05:40 UTC  tenant.example\n  snapshot.organization.member  2\nstore read  14:05:13 UTC  2 tuples  acme\n3 tuples from Fake: 1 missing from the store, 0 already there, 2 there with a different condition\n\nmissing  in Fake, not in the store\n1 tuple\nuser member organization\t1\n+ user:u2 member organization:o1 (in_office)\n\nchanged  condition differs\n2 tuples\nuser member organization\t2\n+ user:u1 member organization:o1 (in_office) was (in_region)\n+ user:u3 member organization:o1 (in_office) was no condition\nstore\t01ARZ3NDEKTSV4RRFFQ69G5FAV\nprovider\tfake\ndry_run\ttrue\ntuples\t3\nmissing\t1\nchanged\t2\nexisting\t0\nwrites\t0\nnot_checked\t0\n"

func TestRenderTuplesShowsTheStoredCondition(t *testing.T) {
	plainReport(t)
	tuple := func(user, cond string, ctx map[string]any) language.Tuple {
		return language.Tuple{Action: language.ActionWrite, User: user, Relation: "member", Object: "organization:acme", Condition: cond, Context: ctx}
	}
	desired := []language.Tuple{
		tuple("user:anne", "new_cond", map[string]any{"k": "v"}),
		tuple("user:bob", "", nil),
		tuple("user:cy", "new_cond", nil),
	}
	was := []language.Tuple{
		tuple("user:anne", "old_cond", map[string]any{"k": "w"}),
		tuple("user:bob", "old_cond", nil),
		tuple("user:cy", "", nil),
	}
	var b bytes.Buffer
	if err := renderTuples(&b, snapshot.Slice(desired), snapshot.Slice(was)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`+ user:anne member organization:acme (new_cond: k="v") was (old_cond: k="w")` + "\n",
		"+ user:bob member organization:acme was (old_cond)\n",
		"+ user:cy member organization:acme (new_cond) was no condition\n",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestDriftPlainSummaryCountsNotCheckedAndPruned(t *testing.T) {
	configtest.Isolate(t)
	plainReport(t)
	pinNow(t, 3*time.Minute)
	fs := newFakeStore(t, conditionedMember("u1", "in_region"), member("gone"), member("gone2"), identityTuple())
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftReportMapping, "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if want := "\nstale\t2\nnot_checked\t1\npruned\t2\nwrites\t1\ndeletes\t3\n"; !strings.Contains(stdout, want) {
		t.Errorf("want %q in\n%q", want, stdout)
	}
}
