package mapping

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const unreadReason = "read with --since: could have removed these for a snapshot.user that was not read; not compared: 2 tuples of user member organization"

// sinceBlockedResult is what --since reads under blockedMapping when u1 was
// blocked before the window: one snapshot.user event, for u2, and the
// memberships of both users, which are always read in full.
func sinceBlockedResult() *snapshot.Result {
	res := blockedResult()
	res.Events = []map[string]any{
		{"type": "snapshot.user", "user_id": "u2", "blocked": false},
		{"type": "snapshot.organization.member", "user_id": "u1", "org": "o1"},
		{"type": "snapshot.organization.member", "user_id": "u2", "org": "o1"},
	}
	res.Counts = []snapshot.Count{
		{Type: "snapshot.user", N: 1, Windowed: true},
		{Type: "snapshot.organization.member", N: 2},
	}
	return res
}

func TestDriftSinceSetsAsideWhatAnUnreadUserMayHaveRemoved(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: sinceBlockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--since", "2h", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, want := range []string{"NOT CHECKED", "blocked", unreadReason} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestBackfillSinceSetsAsideWhatAnUnreadUserMayHaveRemoved(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: sinceBlockedResult()})
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--since", "2h", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, want := range []string{"NOT CHECKED", "blocked", unreadReason} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDriftSincePruneForceLeavesWhatAnUnreadUserMayHaveRemoved(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t, member("u1"), member("u2"))
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: sinceBlockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--since", "2h", "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("writes = %v", fs.writes)
	}
	if strings.Contains(stdout, "STALE") || strings.Contains(stdout, "MISSING") || !strings.Contains(stdout, unreadReason) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDriftSinceJSONListsTheUnreadTuples(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: sinceBlockedResult()})
	got, stderr, err := jsonDrift(t, fs.cli(), driftArgs(t, blockedMapping, "--since", "2h", "--dry-run")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(got.NotChecked) != 1 || got.NotChecked[0].Rule != "blocked" || got.NotChecked[0].Reason != unreadReason {
		t.Fatalf("not_checked = %+v", got.NotChecked)
	}
	ts := got.NotChecked[0].Tuples
	if len(ts) != 2 || ts[0].User != "user:u1" || ts[1].User != "user:u2" {
		t.Errorf("tuples = %+v", ts)
	}
	if len(got.Missing) != 0 {
		t.Errorf("missing = %+v", got.Missing)
	}
}

// ownTuplesMapping patches a user's own memberships on a user event.
const ownTuplesMapping = `version: "1"
rules:
  - name: own
    when: input.type == "snapshot.user"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: patch
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:home"
`

func TestDriftSinceStillWritesTheTuplesOfTheUsersItRead(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	res := sinceBlockedResult()
	res.Events = res.Events[:1]
	res.Counts = res.Counts[:1]
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: res})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, ownTuplesMapping, "--since", "2h", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || !strings.Contains(fs.writes[0], "organization:home") {
		t.Fatalf("writes = %v\n%s", fs.writes, stdout)
	}
	if strings.Contains(stdout, unreadReason) {
		t.Errorf("stdout lists the read user's tuple as not checked:\n%s", stdout)
	}
}

func TestDriftWithoutSinceStillSetsAsideTheBlockedUserOnly(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: userFirstTypes(), result: blockedResult()})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, blockedMapping, "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], "user:u1") {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stdout, "not compared: 1 tuple of user member organization") || strings.Contains(stdout, "--since") {
		t.Errorf("stdout:\n%s", stdout)
	}
}
