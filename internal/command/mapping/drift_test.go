package mapping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const driftMapping = `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "organization.member.added" || input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: organization.member.removed
    when: input.type == "user.deleted"
    action: delete
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: user.updated
    when: input.type == "user.updated"
    tuples:
      - user: "user:{{ input.user }}"
        relation: identity
        object: "connection:{{ input.conn }}"
`

// driftConditionMapping writes the member tuple with a condition.
const driftConditionMapping = `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
        condition: in_office
`

func member(user string) map[string]any {
	return map[string]any{"user": "user:" + user, "relation": "member", "object": "organization:o1"}
}

// memberResult is a snapshot with one member event per user.
func memberResult(users ...string) *snapshot.Result {
	res := fakeResult()
	res.Events = nil
	for _, u := range users {
		res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member", "user": u, "org": "o1"})
	}
	return res
}

func runDrift(t *testing.T, c *cli.CLI, stdin io.Reader, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runSnapshotCmd(t, c, stdin, "drift", args...)
}

func driftArgs(t *testing.T, mapping string, extra ...string) []string {
	t.Helper()
	return append([]string{writeFile(t, t.TempDir(), "m.yaml", mapping), "--provider", "fake"}, extra...)
}

func TestDriftNoDrift(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("u2"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "no drift: 2 tuples match Fake") || !strings.Contains(stdout, "store read") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote with nothing to fix")
	}
}

func TestDriftNoDriftDryRunExitsZero(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	if _, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--dry-run")...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
}

func identityTuple() map[string]any {
	return map[string]any{"user": "user:u9", "relation": "identity", "object": "connection:c1"}
}

func TestDriftReport(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("gone"), identityTuple())
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--dry-run")...)
	if clierr.Code(err) != clierr.CodeTestFailed || !clierr.IsSilent(err) {
		t.Fatalf("code = %d err = %v\n%s", clierr.Code(err), err, stderr)
	}
	for _, want := range []string{"store read", "3 tuples", "acme", "Fake read",
		"missing", "in Fake, not in the store", "+ user:u2 member organization:o1",
		"stale", "in the store, not in Fake", "- user:gone member organization:o1",
		"not checked", "user.updated", "user identity connection"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "user:u9") || strings.Contains(stdout, "condition differs") {
		t.Errorf("identity tuple counted, or empty section printed:\n%s", stdout)
	}
	if fs.writeCount() != 0 {
		t.Fatal("dry run wrote")
	}
}

func TestDriftStoreReadBeforeProvider(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, time.Minute)
	var log []string
	fs := newFakeStore(t, member("u1"))
	fs.log = &log
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1"), log: &log})
	stdout, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(log) < 2 || log[0] != "store read" || log[len(log)-1] != "provider read" {
		t.Fatalf("order = %v", log)
	}
	if !strings.Contains(stdout, "store read  14:03:13 UTC  1 tuple  acme") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDriftMappingProblemCostsNoStoreRead(t *testing.T) {
	configtest.Isolate(t)
	var log []string
	fs := newFakeStore(t)
	fs.log = &log
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	args := driftArgs(t, strings.Replace(driftMapping, ` || input.type == "snapshot.organization.member"`, "", 1))
	if _, _, err := runDrift(t, fs.cli(), nil, args...); err == nil {
		t.Fatal("want an error")
	}
	if len(log) != 0 {
		t.Fatalf("read the store: %v", log)
	}
}

func TestDriftPruneForce(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 2 || !strings.Contains(fs.writes[0], `"writes"`) || !strings.Contains(fs.writes[1], `"deletes"`) {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, want := range []string{"sent 1 write to store acme (Fake state as of 14:05:40 UTC)", "deleted 1 stale tuple from store acme"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestDriftForceWithoutPruneLeavesStale(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if fs.writeCount() != 1 || strings.Contains(fs.writes[0], `"deletes"`) {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stderr, "1 stale tuple left alone; run again with --prune to delete it") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftForceWithoutPruneLeavesSeveralStale(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone1"), member("gone2"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, _ := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...)
	if !strings.Contains(stderr, "2 stale tuples left alone; run again with --prune to delete them") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftNoDriftSingular(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...)
	if err != nil || !strings.Contains(stderr, "no drift: 1 tuple matches Fake") {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
}

func TestDriftDryRunSaysNothingWritten(t *testing.T) {
	const note = "dry run: nothing written"
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, _ := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--dry-run")...)
	if !strings.Contains(stderr, note) || strings.Contains(stderr, "Snapshot is") {
		t.Errorf("stderr:\n%s", stderr)
	}
	c := fs.cli()
	c.JSON = true
	_, stderr, _ = runDrift(t, c, nil, driftArgs(t, driftMapping, "--dry-run")...)
	if strings.Contains(stderr, note) {
		t.Errorf("machine mode said it:\n%s", stderr)
	}
}

func TestDriftForceHasNoAgeLine(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, _ := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force", "--prune")...)
	if strings.Contains(stderr, "Snapshot is") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftSuccessCountsChangedTuples(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftConditionMapping, "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "sent 1 write to store acme, replacing 1 changed tuple (Fake state as of 14:05:40 UTC)") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftStoreLineUsesDatesAcrossMidnight(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	res := memberResult("u1")
	res.StartedAt = time.Date(2026, 11, 19, 23, 59, 50, 0, time.UTC)
	res.FinishedAt = time.Date(2026, 11, 20, 0, 0, 10, 0, time.UTC)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res})
	old := now
	now = func() time.Time { return time.Date(2026, 11, 19, 23, 59, 40, 0, time.UTC) }
	t.Cleanup(func() { now = old })
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"store read  2026-11-19 23:59:40 UTC", "2026-11-19 23:59:50 → 2026-11-20 00:00:10 UTC"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// The provider read fits in one day, but the store was read the day before.
func TestDriftStoreLineIsDatedWhenReadTheDayBefore(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	res := memberResult("u1")
	res.StartedAt = time.Date(2026, 11, 20, 0, 0, 1, 0, time.UTC)
	res.FinishedAt = time.Date(2026, 11, 20, 0, 0, 5, 0, time.UTC)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res})
	old := now
	now = func() time.Time { return time.Date(2026, 11, 19, 23, 59, 58, 0, time.UTC) }
	t.Cleanup(func() { now = old })
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...)
	if err != nil {
		t.Fatal(err)
	}
	if want := "store read  2026-11-19 23:59:58 UTC"; !strings.Contains(stdout, want) {
		t.Errorf("stdout missing %q:\n%s", want, stdout)
	}
}

// The document's times are UTC whatever the machine's zone.
func TestDriftJSONTimesAreUTC(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	zone := time.FixedZone("EET", 2*60*60)
	res := memberResult("u1")
	res.StartedAt, res.FinishedAt = res.StartedAt.In(zone), res.FinishedAt.In(zone)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res})
	old := now
	now = func() time.Time { return snapStart.In(zone) }
	t.Cleanup(func() { now = old })
	c := fs.cli()
	c.JSON = true
	stdout, _, err := runDrift(t, c, nil, driftArgs(t, driftMapping)...)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, stdout)
	}
	for _, k := range []string{"store_read_at", "snapshot_started_at", "snapshot_finished_at"} {
		if v, _ := doc[k].(string); !strings.HasSuffix(v, "Z") {
			t.Errorf("%s = %q, want a UTC time", k, v)
		}
	}
}

func TestSnapshotAge(t *testing.T) {
	res := &snapshot.Result{StartedAt: snapStart}
	for _, tc := range []struct {
		after time.Duration
		want  string
	}{
		{30 * time.Second, "Snapshot is 30s old; changes since then are not reflected."},
		{6 * time.Minute, "Snapshot is 6m old; changes since then are not reflected."},
	} {
		pinNow(t, tc.after)
		if got := snapshotAge(res); got != tc.want {
			t.Errorf("after %v: %q, want %q", tc.after, got, tc.want)
		}
	}
}

func TestDriftProviderProblemCostsNoStoreRead(t *testing.T) {
	configtest.Isolate(t)
	var log []string
	fs := newFakeStore(t)
	fs.log = &log
	providers["fake"] = providerEntry{
		Label: "Fake",
		Types: fakeTypes,
		New:   func(*cobra.Command) (snapshot.Provider, error) { return nil, errors.New("missing domain") },
	}
	t.Cleanup(func() { delete(providers, "fake") })
	if _, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping)...); err == nil || !strings.Contains(err.Error(), "missing domain") {
		t.Fatalf("err = %v", err)
	}
	if len(log) != 0 {
		t.Fatalf("read store: %v", log)
	}
}

func TestDriftForceFixesMissingExitsZero(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	if _, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") {
		t.Fatalf("writes = %v", fs.writes)
	}
}

func TestDriftNonInteractiveReportsOnly(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runDrift(t, c, nil, driftArgs(t, driftMapping)...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	var got driftResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.StoreReadAt.IsZero() || got.StartedAt.IsZero() || got.FinishedAt.IsZero() ||
		len(got.Missing) != 1 || len(got.Stale) != 1 || got.Changed == nil || got.Sent != (sentOut{}) {
		t.Fatalf("result = %+v", got)
	}
	if !strings.Contains(stderr, "report only: pass --force to write") || fs.writeCount() != 0 {
		t.Errorf("writes = %d\n%s", fs.writeCount(), stderr)
	}
}

func TestDriftEmptySnapshotKeepsTuplesWithoutPrune(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("a"), member("b"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult()})
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(stdout, "2 tuples") || fs.writeCount() != 0 {
		t.Errorf("writes = %d\n%s", fs.writeCount(), stdout)
	}
}

func TestDriftProviderErrorComputesNothing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("a"))
	boom := errors.New("tenant unreachable")
	useFakeProvider(t, fakeProvider{types: fakeTypes(), err: boom})
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force", "--prune")...)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(stdout, "stale") || strings.Contains(stdout, "missing") || fs.writeCount() != 0 {
		t.Errorf("writes = %d\n%s", fs.writeCount(), stdout)
	}
}

func TestDriftCanceledDuringSnapshot(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("a"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), err: context.Canceled})
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...)
	if clierr.Code(err) != clierr.CodeCanceled {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if strings.Contains(stdout, "missing") || strings.Contains(stdout, "stale") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDriftPartialFailure(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	fs.failFrom = 2
	users := make([]string, 250)
	for i := range users {
		users[i] = fmt.Sprintf("u%03d", i)
	}
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult(users...)})
	c := fs.cli()
	c.JSON = true
	stdout, _, err := runDrift(t, c, nil, driftArgs(t, driftMapping, "--force")...)
	if k := clierr.NewEnvelope(err).Error.Code; k != clierr.KindPartialFailure {
		t.Fatalf("kind = %v err = %v", k, err)
	}
	if msg := clierr.NewEnvelope(err).Error.Message; !strings.Contains(msg, "sent 100 of 250 changes before the failure; run the command again to send the rest") {
		t.Errorf("message = %q", msg)
	}
	var got driftResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", jerr, stdout)
	}
	if got.Sent.Writes != 100 {
		t.Fatalf("sent = %+v", got.Sent)
	}
}

func TestDriftCapsLongLists(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	users := make([]string, 80)
	for i := range users {
		users[i] = fmt.Sprintf("u%02d", i)
	}
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult(users...)})
	stdout, _, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--dry-run")...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(stdout, "+ user:"); n != 50 || !strings.Contains(stdout, "… 30 more; -o json lists them all") {
		t.Errorf("tuple lines = %d\n%s", n, stdout)
	}
}

func TestDriftWithoutTerminalReportsOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		noInput bool
	}{{"no terminal", false}, {"no-input", true}} {
		t.Run(tc.name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t, member("gone"))
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			c := fs.cli()
			c.NoInput = tc.noInput
			_, stderr, err := runDrift(t, c, strings.NewReader(""), driftArgs(t, driftMapping)...)
			if clierr.Code(err) != clierr.CodeTestFailed || !clierr.IsSilent(err) {
				t.Fatalf("code = %d err = %v", clierr.Code(err), err)
			}
			if !strings.Contains(stderr, "report only: pass --force to write") || fs.writeCount() != 0 {
				t.Errorf("writes = %d\n%s", fs.writeCount(), stderr)
			}
		})
	}
}

func TestDriftPartialPruneCountsDeletesSent(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	for i := range 250 {
		fs.tuples = append(fs.tuples, member(fmt.Sprintf("gone%03d", i)))
	}
	fs.failFrom = 2
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult()})
	c := fs.cli()
	c.JSON = true
	stdout, _, err := runDrift(t, c, nil, driftArgs(t, driftMapping, "--prune", "--force")...)
	if msg := clierr.NewEnvelope(err).Error.Message; !strings.Contains(msg, "sent 100 of 250 changes before the failure; run the command again to send the rest") {
		t.Errorf("message = %q", msg)
	}
	var got driftResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", jerr, stdout)
	}
	if got.Sent.Deletes != 100 || got.Pruned != 100 {
		t.Fatalf("sent = %+v pruned = %d, want 100 of each", got.Sent, got.Pruned)
	}
}

func TestDriftCanceledDuringWrite(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.onWrite = func(n int) bool {
		if n == 2 {
			cancel()
		}
		return n == 2
	}
	users := make([]string, 250)
	for i := range users {
		users[i] = fmt.Sprintf("u%03d", i)
	}
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult(users...)})
	root := New(fs.cli()).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"drift"}, driftArgs(t, driftMapping, "--force")...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err := root.ExecuteContext(ctx)
	if clierr.Code(err) != clierr.CodeCanceled {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(err.Error(), "sent 100 of 250 changes before the failure") {
		t.Errorf("err = %v", err)
	}
}

func TestFailureReportPrintsTheErrorOnce(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "http://store.example/write", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	var buf bytes.Buffer
	_ = reportSnapshotFailure(context.Background(), &buf, false, "sent 0 of 1 write before the failure", snapshot.Sent{}, applyHints(err), err)
	if n := strings.Count(buf.String(), "connection refused"); n != 1 {
		t.Errorf("error printed %d times:\n%s", n, buf.String())
	}
}
