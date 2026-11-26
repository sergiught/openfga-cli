package mapping

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// blockedMapping deletes a blocked user's memberships on a user event and
// writes memberships on member events, which are read after the users.
const blockedMapping = `version: "1"
rules:
  - name: blocked
    when: input.type == "snapshot.user" && input.blocked == true
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: member
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
`

// roleMapping patches all of a user's roles with the one role an event names.
const roleMapping = `version: "1"
rules:
  - name: role
    when: input.type == "snapshot.organization.member.role"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "role:"
        action: patch
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: assignee
        object: "role:{{ input.role }}"
`

// blockedResult has a blocked user u1 and a user u2, both members of o1. With
// bothBlocked, u2 is blocked too.
func blockedResult(bothBlocked ...bool) *snapshot.Result {
	res := fakeResult()
	res.Events = nil
	for _, u := range []string{"u1", "u2"} {
		res.Events = append(res.Events, map[string]any{"type": "snapshot.user", "user_id": u, "blocked": u == "u1" || len(bothBlocked) > 0})
	}
	for _, u := range []string{"u1", "u2"} {
		res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member", "user_id": u, "org": "o1"})
	}
	return res
}

func roleResult() *snapshot.Result {
	res := fakeResult()
	res.Events = nil
	for _, r := range []string{"rol_a", "rol_b"} {
		res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member.role", "user_id": "u1", "role": r})
	}
	return res
}

func roleTuple(role string) map[string]any {
	return map[string]any{"user": "user:u1", "relation": "assignee", "object": "role:" + role}
}

func roleTypes() []snapshot.Type {
	return append(fakeTypes(), snapshot.Type{Name: "snapshot.organization.member.role", Mirrors: "organization.member.role.added"})
}

func TestDriftForceWritesOnlyTheTupleReplayCanVouchFor(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], "user:u1") {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, want := range []string{"NOT CHECKED", "1 rule", "blocked",
		"removes tuples that other events write, and a snapshot does not say which happened last; not compared: 1 tuple of user member organization"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDriftPruneForceLeavesTheContestedStoredTupleAlone(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("u2"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	if strings.Contains(stdout, "STALE") || strings.Contains(stdout, "MISSING") || !strings.Contains(stdout, "NOT CHECKED") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "no drift: 1 tuple matches Fake") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftJSONListsTheContestedTuples(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	got, stderr, err := jsonDrift(t, fs.cli(), driftArgs(t, blockedMapping, "--dry-run")...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(got.NotChecked) != 1 || got.NotChecked[0].Rule != "blocked" || !strings.Contains(got.NotChecked[0].Reason, "not compared: 1 tuple") {
		t.Fatalf("not_checked = %+v", got.NotChecked)
	}
	if ts := got.NotChecked[0].Tuples; len(ts) != 1 || ts[0].User != "user:u1" || ts[0].Relation != "member" || ts[0].Object != "organization:o1" {
		t.Errorf("tuples = %+v", ts)
	}
	if len(got.Missing) != 1 || got.Missing[0].User != "user:u2" {
		t.Errorf("missing = %+v", got.Missing)
	}
}

func TestDriftNotCheckedWithoutTuplesOmitsTheField(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	c := fs.cli()
	c.JSON = true
	stdout, _, _ := runDrift(t, c, nil, driftArgs(t, driftMapping, "--dry-run")...)
	var raw struct {
		NotChecked []map[string]json.RawMessage `json:"not_checked"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil || len(raw.NotChecked) == 0 {
		t.Fatalf("err = %v\n%s", err, stdout)
	}
	if _, ok := raw.NotChecked[0]["tuples"]; ok {
		t.Errorf("entry has tuples:\n%s", stdout)
	}
}

func TestDriftPlainCountsContestedRules(t *testing.T) {
	configtest.Isolate(t)
	plainReport(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--dry-run")...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, want := range []string{"\nnot checked\n  blocked  removes tuples", "\nnot_checked\t1\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDriftPruneForceLeavesTuplesOfEventsThatRemoveEachOther(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, roleTuple("rol_a"), roleTuple("rol_b"))
	useFakeProvider(t, fakeProvider{types: roleTypes(), result: roleResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, roleMapping, "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stdout, "NOT CHECKED") || !strings.Contains(stdout, "not compared: 2 tuples of user assignee role") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestBackfillForceWritesOnlyTheTupleReplayCanVouchFor(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], "user:u1") {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stdout, "NOT CHECKED") || !strings.Contains(stdout, "blocked") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestBackfillJSONListsNotChecked(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runBackfill(t, c, nil, writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var got backfillResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.NotChecked) != 1 || got.NotChecked[0].Rule != "blocked" || len(got.NotChecked[0].Tuples) != 1 || got.NotChecked[0].Tuples[0].User != "user:u1" {
		t.Errorf("not_checked = %+v", got.NotChecked)
	}
	if len(got.Missing) != 1 || got.Missing[0].User != "user:u2" {
		t.Errorf("missing = %+v", got.Missing)
	}

	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	stdout, _, err = runBackfill(t, c, nil, writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping), "--provider", "fake", "--dry-run")
	if err != nil || !strings.Contains(stdout, `"not_checked": []`) {
		t.Errorf("err = %v\n%s", err, stdout)
	}
}

func TestBackfillPlainCountsNotChecked(t *testing.T) {
	configtest.Isolate(t)
	plainReport(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, want := range []string{"\nnot checked\n  blocked  removes tuples", "\nwrites\t0\nnot_checked\t1\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestBackfillWritesNothingWhenEveryTupleIsContested(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult(true)})
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stdout, "NOT CHECKED") || !strings.Contains(stdout, "not compared: 2 tuples of user member organization") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDriftForceDoesNotReplaceAContestedTupleStoredWithAnotherCondition(t *testing.T) {
	configtest.Isolate(t)
	// u1's stored membership is unconditional, while the rule would write it
	// with a condition. It is contested, so it is neither changed nor replaced.
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult()})
	mapping := strings.Replace(blockedMapping, `object: "organization:{{ input.org }}"`, "object: \"organization:{{ input.org }}\"\n        condition: in_office", 1)
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, mapping, "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], "user:u1") {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, w := range fs.writes {
		if strings.Contains(w, "deletes") {
			t.Errorf("a delete was sent: %s", w)
		}
	}
	if strings.Contains(stdout, "CHANGED") || !strings.Contains(stdout, "not compared: 1 tuple of user member organization") {
		t.Errorf("stdout:\n%s", stdout)
	}
}
