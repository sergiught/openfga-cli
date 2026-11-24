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
