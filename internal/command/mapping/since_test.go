package mapping

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

func TestParseSince(t *testing.T) {
	pinNow(t, 5*24*time.Hour) // 2026-11-24 14:02:13 UTC
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{in: "2h", want: snapStart.Add(5*24*time.Hour - 2*time.Hour)},
		{in: "2026-11-24T09:00:00Z", want: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)},
		{in: ""},
		{in: "0", wantErr: true},
		{in: "-1h", wantErr: true},
		{in: "90", wantErr: true},
		{in: "tomorrow", wantErr: true},
		{in: "2026-11-24T14:02:14Z", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseSince(tc.in)
		if tc.wantErr {
			if err == nil || clierr.Code(err) != clierr.CodeUsage {
				t.Errorf("parseSince(%q) err = %v, want a usage error", tc.in, err)
			}
			continue
		}
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

// windowedTypes is fakeTypes with snapshot.user readable by window.
func windowedTypes() []snapshot.Type {
	ts := fakeTypes()
	for i := range ts {
		if ts[i].Name == "snapshot.user" {
			ts[i].Windowed = true
		}
	}
	return ts
}

func windowedResult() *snapshot.Result {
	return &snapshot.Result{
		Source:     "tenant.example",
		Events:     []map[string]any{{"type": "snapshot.user", "user": "u1"}},
		Counts:     []snapshot.Count{{Type: "snapshot.user", N: 1, Windowed: true}},
		StartedAt:  snapStart,
		FinishedAt: snapFinish,
	}
}

const userSnapshotMapping = `version: "1"
rules:
  - name: user.created
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:o1"
`

func TestBackfillSincePassesTheWindowToTheProvider(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	var gotSince time.Time
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: windowedResult(), gotSince: &gotSince})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)
	want := snapStart.Add(-2 * time.Hour)

	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !gotSince.Equal(want) {
		t.Errorf("provider since = %v, want %v", gotSince, want)
	}
	if !strings.Contains(stdout, "snapshot.user  1  (changed since 2026-11-19 12:02:13 UTC)\n") {
		t.Errorf("stdout:\n%s", stdout)
	}

	c := fs.cli()
	c.JSON = true
	stdout, stderr, err = runBackfill(t, c, nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var doc struct {
		Since string `json:"since"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if doc.Since != "2026-11-19T12:02:13Z" {
		t.Errorf("since = %q in\n%s", doc.Since, stdout)
	}
}

func TestBackfillWithoutSinceOmitsIt(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	gotSince := time.Unix(1, 0)
	res := windowedResult()
	res.Counts[0].Windowed = false // a provider reads whole without a window
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: res, gotSince: &gotSince})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)

	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !gotSince.IsZero() || strings.Contains(stdout, "changed since") {
		t.Errorf("since = %v, stdout:\n%s", gotSince, stdout)
	}

	c := fs.cli()
	c.JSON = true
	stdout, stderr, err = runBackfill(t, c, nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if _, ok := doc["since"]; ok {
		t.Errorf("document has since:\n%s", stdout)
	}
}

func TestBackfillSinceNeedsAWindowedRule(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: fakeResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)

	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if clierr.Code(err) != clierr.CodeUsage || err == nil || !strings.Contains(err.Error(), "--since narrows only snapshot.user") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if calls != 0 || len(fs.readRequests()) != 0 || fs.writeCount() != 0 {
		t.Errorf("calls = %d reads = %d writes = %d", calls, len(fs.readRequests()), fs.writeCount())
	}
}

func TestBackfillSinceUnsupportedProvider(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)

	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if clierr.Code(err) != clierr.CodeUsage || err == nil || !strings.Contains(err.Error(), "--since is not supported by Fake") || calls != 0 {
		t.Fatalf("code = %d err = %v calls = %d", clierr.Code(err), err, calls)
	}
}

func TestBackfillSinceBadValueFailsBeforeAnything(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: windowedResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)

	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "tomorrow", "--dry-run")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if calls != 0 || len(fs.readRequests()) != 0 || fs.writeCount() != 0 {
		t.Errorf("calls = %d reads = %d writes = %d", calls, len(fs.readRequests()), fs.writeCount())
	}
}

func TestBackfillSinceFailedWindowWritesNothing(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: windowedTypes(), err: errors.New("tenant unreachable")})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)

	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--force")
	if err == nil || fs.writeCount() != 0 {
		t.Fatalf("err = %v writes = %d", err, fs.writeCount())
	}
}

func TestDriftSincePassesTheWindowToTheProvider(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	var gotSince time.Time
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: windowedResult(), gotSince: &gotSince})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)
	want := snapStart.Add(-2 * time.Hour)

	stdout, stderr, err := runDrift(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !gotSince.Equal(want) {
		t.Errorf("provider since = %v, want %v", gotSince, want)
	}
	if !strings.Contains(stdout, "snapshot.user  1  (changed since 2026-11-19 12:02:13 UTC)\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// userAndMemberMapping has a user rule for a windowed type and a member rule
// for a whole one, each writing a shape of its own.
const userAndMemberMapping = `version: "1"
rules:
  - name: user.created
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "tenant:t1"
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:o1"
`

func userAndMemberResult() *snapshot.Result {
	res := windowedResult()
	res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member", "user": "u1", "org": "o1"})
	res.Counts = append(res.Counts, snapshot.Count{Type: "snapshot.organization.member", N: 1})
	return res
}

func staleTuples() []map[string]any {
	return []map[string]any{
		{"user": "user:gone", "relation": "member", "object": "tenant:t1"},
		{"user": "user:gone", "relation": "member", "object": "organization:o1"},
	}
}

func TestDriftSinceListsWindowedShapesAsNotChecked(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	var log []string
	fs := newFakeStore(t, staleTuples()...)
	fs.log = &log
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: userAndMemberResult(), log: &log})
	mp := writeFile(t, t.TempDir(), "m.yaml", userAndMemberMapping)

	stdout, stderr, err := runDrift(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(log) != 2 || log[0] != "store read" || log[1] != "provider read" {
		t.Errorf("order = %v, want the whole store read before the provider", log)
	}
	from, to := strings.Index(stdout, "\nstale"), strings.Index(stdout, "\nnot checked")
	if from < 0 || to < from {
		t.Fatalf("want a stale section before not checked:\n%s", stdout)
	}
	if stale := stdout[from:to]; !strings.Contains(stale, "organization:o1") || strings.Contains(stale, "tenant:t1") {
		t.Errorf("stale section:\n%s", stale)
	}
	want := "read with --since: only snapshot.user changed since 2026-11-19 12:02:13 UTC; not compared: user member tenant"
	if !strings.Contains(stdout, "\nnot checked\n") || !strings.Contains(stdout, "user.created") || !strings.Contains(stdout, want) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDriftSincePruneLeavesWindowedShapes(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t, staleTuples()...)
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: userAndMemberResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", userAndMemberMapping)

	_, stderr, err := runDrift(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--prune", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	deletes := 0
	for _, w := range fs.writes {
		if !strings.Contains(w, `"deletes"`) {
			continue
		}
		deletes++
		if !strings.Contains(w, "organization:o1") || strings.Contains(w, "tenant:t1") {
			t.Errorf("delete request: %s", w)
		}
	}
	if deletes != 1 {
		t.Errorf("delete requests = %d in %v", deletes, fs.writes)
	}
}

func TestDriftSinceUsersOnlyReadsTheStoreAfterTheProvider(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	var log []string
	fs := newFakeStore(t, map[string]any{"user": "user:gone", "relation": "member", "object": "tenant:t1"})
	fs.log = &log
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: windowedResult(), log: &log})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)

	stdout, stderr, err := runDrift(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(log) == 0 || log[0] != "provider read" {
		t.Errorf("order = %v, want the provider read first", log)
	}
	header, storeLine := strings.Index(stdout, "Fake read"), strings.Index(stdout, "store read")
	if header < 0 || storeLine < header {
		t.Errorf("want the provider header before the store line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "\nmissing") || !strings.Contains(stdout, "user:u1") || !strings.Contains(stdout, "organization:o1") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "\nstale") {
		t.Errorf("a users-only run reported stale:\n%s", stdout)
	}
}

func TestDriftSinceNothingChangedReadsNoTuples(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t, staleTuples()...)
	res := windowedResult()
	res.Events = nil
	res.Counts[0].N = 0
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: res})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)

	stdout, stderr, err := runDrift(t, fs.cli(), nil, mp, "--provider", "fake", "--since", "2h", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if n := len(fs.readRequests()); n != 0 {
		t.Errorf("store reads = %d, want 0", n)
	}
	if !strings.Contains(stderr, "no drift") || strings.Contains(stdout, "store read") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
