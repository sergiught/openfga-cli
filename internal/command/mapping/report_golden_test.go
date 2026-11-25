package mapping

import (
	"strings"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/configtest"
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

const driftPlainGolden = "store read  14:05:13 UTC  4 tuples  acme\nFake read  14:02:13 → 14:05:40 UTC  tenant.example\n  snapshot.organization.member  2\n\nmissing  in Fake, not in the store\n1 tuple\nuser member organization\t1\n+ user:u2 member organization:o1 (in_office)\n\nchanged  condition differs\n2 tuples\nuser member organization\t2\n+ user:u1 member organization:o1 (in_office)\n+ user:u3 member organization:o1 (in_office)\n\nstale    in the store, not in Fake\n1 tuple\nuser member organization\t1\n- user:gone member organization:o1\n\nnot checked\n  user.updated  no snapshot rule writes user identity connection\nstore\t01ARZ3NDEKTSV4RRFFQ69G5FAV\nprovider\tfake\ndry_run\ttrue\nmissing\t1\nchanged\t2\nstale\t1\nwrites\t0\ndeletes\t0\n"
const backfillPlainGolden = "Fake read  14:02:13 → 14:05:40 UTC  tenant.example\n  snapshot.organization.member  2\nstore read  14:05:13 UTC  2 tuples  acme\n3 tuples from Fake: 1 missing from the store, 0 already there, 2 there with a different condition\n\nmissing  in Fake, not in the store\n1 tuple\nuser member organization\t1\n+ user:u2 member organization:o1 (in_office)\n\nchanged  condition differs\n2 tuples\nuser member organization\t2\n+ user:u1 member organization:o1 (in_office)\n+ user:u3 member organization:o1 (in_office)\nstore\t01ARZ3NDEKTSV4RRFFQ69G5FAV\nprovider\tfake\ndry_run\ttrue\ntuples\t3\nmissing\t1\nchanged\t2\nexisting\t0\nwrites\t0\n"
