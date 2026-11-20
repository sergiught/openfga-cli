package mapping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/agentmode"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const memberSnapshotMapping = `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "organization.member.added" || input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
      - user: "user:{{ input.user }}"
        relation: reader
        object: "organization:{{ input.org }}"
`

var (
	snapStart  = time.Date(2026, 11, 19, 14, 2, 13, 0, time.UTC)
	snapFinish = time.Date(2026, 11, 19, 14, 5, 40, 0, time.UTC)
)

func fakeTypes() []snapshot.Type {
	return []snapshot.Type{
		{Name: "snapshot.organization", Mirrors: "organization.created"},
		{Name: "snapshot.organization.member", Mirrors: "organization.member.added"},
		{Name: "snapshot.user", Mirrors: "user.created"},
	}
}

func fakeResult() *snapshot.Result {
	return &snapshot.Result{
		Source: "tenant.example",
		Events: []map[string]any{
			{"type": "snapshot.organization.member", "user": "u1", "org": "o1"},
			{"type": "snapshot.organization.member", "user": "u2", "org": "o1"},
		},
		Counts:     []snapshot.Count{{Type: "snapshot.organization.member", N: 2}},
		StartedAt:  snapStart,
		FinishedAt: snapFinish,
	}
}

// pinNow makes the command's clock read d after the snapshot started.
func pinNow(t *testing.T, d time.Duration) {
	t.Helper()
	old := now
	now = func() time.Time { return snapStart.Add(d) }
	t.Cleanup(func() { now = old })
}

func runBackfill(t *testing.T, c *cli.CLI, stdin io.Reader, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runSnapshotCmd(t, c, stdin, "backfill", args...)
}

// runSnapshotCmd runs one of the snapshot commands, keeping stdout and stderr
// apart.
func runSnapshotCmd(t *testing.T, c *cli.CLI, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := New(c).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetArgs(append([]string{name}, args...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func TestBackfillDryRun(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, want := range []string{"Fake read", "14:02:13 → 14:05:40 UTC", "tenant.example", "4 tuples",
		"user member organization", "user reader organization", "+ user:u1 member organization:o1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if fs.writeCount() != 0 {
		t.Fatalf("dry run sent %d writes", fs.writeCount())
	}
	if !strings.Contains(stderr, "dry run: nothing written") || strings.Contains(stderr, "Snapshot is") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestBackfillForceWrites(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", fs.writeCount())
	}
	for _, want := range []string{`"user:u1"`, `"user:u2"`, `"member"`, `"reader"`, `"organization:o1"`} {
		if !strings.Contains(fs.writes[0], want) {
			t.Errorf("write missing %s:\n%s", want, fs.writes[0])
		}
	}
	if !strings.Contains(stderr, "wrote 4 tuples to store acme; 0 already there (Fake state as of 14:05:40 UTC)") || strings.Contains(stderr, "Snapshot is") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// storedTuple is a tuple of memberSnapshotMapping as the store holds it.
func storedTuple(user, relation string) map[string]any {
	return map[string]any{"user": "user:" + user, "relation": relation, "object": "organization:o1"}
}

func TestBackfillRerunWritesNothing(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 60*time.Second)
	fs := newFakeStore(t, storedTuple("u1", "member"), storedTuple("u1", "reader"), storedTuple("u2", "member"), storedTuple("u2", "reader"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.NoInput = true // nothing to confirm, so no --force is needed
	stdout, stderr, err := runBackfill(t, c, strings.NewReader(""), mp, "--provider", "fake")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("wrote %d times with nothing missing", fs.writeCount())
	}
	if !strings.Contains(stdout, "4 tuples from Fake: 0 missing from the store, 4 already there") || !strings.Contains(stdout, "store read  14:03:13 UTC  4 tuples  acme") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "nothing to write: 4 tuples already in store acme (Fake state as of 14:05:40 UTC)") || strings.Contains(stderr, "Snapshot is") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestBackfillWritesOnlyMissing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, storedTuple("u1", "member"), storedTuple("u1", "reader"), storedTuple("u2", "member"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", fs.writeCount())
	}
	if w := fs.writes[0]; strings.Count(w, `"user":`) != 1 || !strings.Contains(w, `"user:u2"`) || !strings.Contains(w, `"reader"`) {
		t.Errorf("write carries more than the one missing tuple:\n%s", w)
	}
	if !strings.Contains(stdout, "4 tuples from Fake: 1 missing from the store, 3 already there") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "wrote 1 tuple to store acme; 3 already there (Fake state as of 14:05:40 UTC)") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestBackfillLeavesChangedTuples(t *testing.T) {
	configtest.Isolate(t)
	changed := storedTuple("u1", "member")
	changed["condition"] = map[string]any{"name": "in_office"}
	fs := newFakeStore(t, changed)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)

	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, w := range fs.writes {
		if strings.Contains(w, `{"user":"user:u1","relation":"member","object":"organization:o1"}`) {
			t.Errorf("changed tuple was written:\n%s", w)
		}
	}
	if strings.Count(fs.writes[0], `"user":`) != 3 {
		t.Errorf("write should carry the 3 missing tuples:\n%s", fs.writes[0])
	}
	for _, want := range []string{"3 missing from the store, 0 already there, 1 there with a different condition", "condition differs"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, `1 tuple is in the store with a different condition; backfill leaves it, "ofga mappings drift" replaces it`) {
		t.Errorf("stderr:\n%s", stderr)
	}

	c := fs.cli()
	c.JSON = true
	stdout, stderr, err = runBackfill(t, c, nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var got backfillResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Changed) != 1 || got.Changed[0].User != "user:u1" || got.Changed[0].Relation != "member" || len(got.Missing) != 3 || got.Existing != 0 {
		t.Fatalf("result = %+v", got)
	}
}

func TestBackfillDryRunDocumentReadsStore(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, time.Minute)
	fs := newFakeStore(t, storedTuple("u1", "member"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runBackfill(t, c, nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatal("dry run wrote")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"store_read_at", "missing", "changed", "existing"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("document has no %q:\n%s", k, stdout)
		}
	}
	if string(raw["changed"]) != "[]" {
		t.Errorf("changed = %s, want []", raw["changed"])
	}
	var got backfillResult
	_ = json.Unmarshal([]byte(stdout), &got)
	if !got.StoreReadAt.Equal(snapStart.Add(time.Minute)) || len(got.Missing) != 3 || got.Existing != 1 || got.Tuples == nil || len(got.Tuples) != 4 {
		t.Fatalf("result = %+v", got)
	}
}

func TestBackfillMachineNeedsFlagEvenWhenNothingIsMissing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, storedTuple("u1", "member"), storedTuple("u1", "reader"), storedTuple("u2", "member"), storedTuple("u2", "reader"))
	log := []string{}
	fs.log = &log
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.YAML = true
	_, _, err := runBackfill(t, c, nil, mp, "--provider", "fake")
	if clierr.Code(err) != clierr.CodeUsage || len(log) != 0 {
		t.Fatalf("code = %d err = %v reads = %v", clierr.Code(err), err, log)
	}
}

func TestBackfillNoInputNeedsForce(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.NoInput = true
	_, stderr, err := runBackfill(t, c, strings.NewReader(""), mp, "--provider", "fake")
	if clierr.NewEnvelope(err).Error.Code != clierr.KindConfirmationRequired || fs.writeCount() != 0 || strings.Contains(stderr, "Snapshot is") {
		t.Fatalf("err = %v writes = %d\n%s", err, fs.writeCount(), stderr)
	}
}

func TestBackfillMachineNeedsFlag(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.JSON = true
	_, _, err := runBackfill(t, c, nil, mp, "--provider", "fake")
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(clierr.NewEnvelope(err).Error.Hint, "--force") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if calls != 0 {
		t.Fatal("read the provider before refusing")
	}
}

func TestBackfillMachineDocument(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runBackfill(t, c, nil, mp, "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var got backfillResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Provider != "fake" || got.Source != "tenant.example" || len(got.Tuples) != 4 || got.Sent.Writes != 4 ||
		len(got.Counts) != 1 || got.Counts[0].Count != 2 || got.DryRun {
		t.Fatalf("result = %+v", got)
	}
}

func TestBackfillNoSnapshotRules(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "auth0.yaml", strings.Replace(memberSnapshotMapping,
		` || input.type == "snapshot.organization.member"`, "", 1))
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	stdout = ansi.Strip(stdout)
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	want := "✗ " + mp + " has no snapshot rules, so there is nothing to check\n" +
		"  these rules can opt in by adding a clause to `when`:\n" +
		"    organization.member.added || input.type == \"snapshot.organization.member\"\n" +
		"  see https://sergiught.github.io/openfga-cli/guide/mappings/#checking-for-drift-and-backfilling\n"
	if stdout != want || stderr != "" {
		t.Errorf("stdout = %q\nstderr = %q\nwant stdout = %q", stdout, stderr, want)
	}
	if calls != 0 || fs.writeCount() != 0 {
		t.Fatalf("calls = %d writes = %d", calls, fs.writeCount())
	}
}

func TestBackfillNoSnapshotRulesNoSuggestion(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", `version: "1"
rules:
  - name: unrelated
    when: input.type == "something.else"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
`)
	stdout, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	stdout = ansi.Strip(stdout)
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	want := "  add a rule whose when names one of: snapshot.organization, snapshot.organization.member, snapshot.user\n"
	if !strings.Contains(stdout, want) || strings.Contains(stdout, "these rules can opt in") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestSnapshotUnknownTypeFails(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	calls := 0
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), calls: &calls})
	mp := writeFile(t, t.TempDir(), "m.yaml", strings.Replace(memberSnapshotMapping,
		"snapshot.organization.member", "snapshot.organisation.member", 1))
	stdout, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	stdout = ansi.Strip(stdout)
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(stdout, "names snapshot types Fake does not have: snapshot.organisation.member") ||
		!strings.Contains(stdout, "snapshot.organization, snapshot.organization.member, snapshot.user") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if calls != 0 {
		t.Fatal("provider was called")
	}
}

func TestBackfillProviderRequired(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--force")
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--provider") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

// A missing --provider is a usage error even when the mapping file is missing
// too: the flag does not depend on the file.
func TestBackfillProviderRequiredBeforeTheFileIsRead(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	_, _, err := runBackfill(t, fs.cli(), nil, filepath.Join(t.TempDir(), "absent.yaml"), "--force")
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--provider") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

func TestBackfillUnknownProvider(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "okta", "--force")
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(clierr.NewEnvelope(err).Error.Hint, "auth0") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

func TestBackfillProviderErrorWritesNothing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	boom := errors.New("tenant unreachable")
	useFakeProvider(t, fakeProvider{types: fakeTypes(), err: boom})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote after a failed read")
	}
}

func TestBackfillRequestsOnlyNamedTypes(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	var got []string
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), gotTypes: &got})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	if _, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run"); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(got) != 1 || got[0] != "snapshot.organization.member" {
		t.Fatalf("types = %v", got)
	}
}

func TestBackfillNothingToWrite(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	empty := fakeResult()
	empty.Events = nil
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: empty})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "nothing to write") || fs.writeCount() != 0 {
		t.Errorf("writes = %d\n%s", fs.writeCount(), stderr)
	}
}

func TestBackfillCapsTupleLines(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	res := fakeResult()
	res.Events = nil
	for i := range 30 { // 30 users x 2 tuples = 60 lines
		res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member", "user": "u" + string(rune('a'+i)), "org": "o1"})
	}
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	stdout, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "60 tuples") || !strings.Contains(stdout, "… 10 more; -o json lists them all") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestBackfillReportsPartialWrite(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if clierr.Code(err) != clierr.CodeError || !clierr.IsSilent(err) {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(stderr, "sent 0 of 4 writes before the failure; run the command again to send the rest") ||
		!strings.Contains(stderr, "type not found") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestBackfillHintsOnlyExplainConditionConflicts(t *testing.T) {
	err := backfillHints(errors.New("plain"))
	if len(err) != 0 {
		t.Fatalf("hints = %v", err)
	}
}

func TestAuth0FlagsNeedCredentials(t *testing.T) {
	configtest.Isolate(t)
	for _, v := range []string{"AUTH0_DOMAIN", "AUTH0_CLIENT_ID", "AUTH0_CLIENT_SECRET"} {
		t.Setenv(v, "")
	}
	fs := newFakeStore(t)
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, _, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "auth0", "--force")
	if clierr.Code(err) != clierr.CodeUsage ||
		!strings.Contains(err.Error(), "--auth0-domain") || !strings.Contains(err.Error(), "AUTH0_DOMAIN") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

func auth0Cmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	auth0Entry().AddFlags(cmd.Flags())
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestAuth0SecretFileTrimmed(t *testing.T) {
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("AUTH0_CLIENT_ID", "")
	t.Setenv("AUTH0_CLIENT_SECRET", "from-env")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := auth0Config(auth0Cmd(t, "--auth0-domain", "t.example", "--auth0-client-id", "id",
		"--auth0-client-secret-file", secret, "--auth0-tenant", "acme"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientSecret != "abc" || cfg.Domain != "t.example" || cfg.ClientID != "id" || cfg.Tenant != "acme" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestAuth0EmptySecretFileIsAUsageError(t *testing.T) {
	t.Setenv("AUTH0_CLIENT_SECRET", "from-env")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := auth0Config(auth0Cmd(t, "--auth0-domain", "t.example", "--auth0-client-id", "id", "--auth0-client-secret-file", secret))
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

func TestAuth0FlagsBeatEnvironment(t *testing.T) {
	t.Setenv("AUTH0_DOMAIN", "env.example")
	t.Setenv("AUTH0_CLIENT_ID", "env-id")
	t.Setenv("AUTH0_CLIENT_SECRET", "env-secret")
	cfg, err := auth0Config(auth0Cmd(t, "--auth0-domain", "flag.example"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Domain != "flag.example" || cfg.ClientID != "env-id" || cfg.ClientSecret != "env-secret" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// TestHintAnchorExistsInGuide pins the link the "no snapshot rules" error
// prints to a heading in the guide, slugged the way the docs site does.
func TestHintAnchorExistsInGuide(t *testing.T) {
	_, fragment, ok := strings.Cut(guideURL, "#")
	if !ok {
		t.Fatalf("guideURL %q has no fragment", guideURL)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "site", "src", "content", "docs", "guide", "mappings.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, line := range strings.Split(string(data), "\n") {
		if heading, found := strings.CutPrefix(line, "### "); found {
			slugs = append(slugs, strings.ReplaceAll(strings.ToLower(strings.TrimSpace(heading)), " ", "-"))
		}
	}
	for _, s := range slugs {
		if s == fragment {
			return
		}
	}
	t.Fatalf("no ### heading in the guide slugs to %q; headings slug to %v", fragment, slugs)
}

func TestBackfillFailureCountsMissingTuples(t *testing.T) {
	configtest.Isolate(t)
	res := fakeResult()
	res.Events = nil
	var stored []map[string]any
	for i := range 120 { // 240 desired tuples
		u := fmt.Sprintf("u%03d", i)
		res.Events = append(res.Events, map[string]any{"type": "snapshot.organization.member", "user": u, "org": "o1"})
		if i < 30 { // 60 already there, so 180 are missing: two requests
			stored = append(stored, storedTuple(u, "member"), storedTuple(u, "reader"))
		}
	}
	fs := newFakeStore(t, stored...)
	fs.failAt = map[int]bool{2: true}
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if err == nil || fs.writeCount() != 2 {
		t.Fatalf("err = %v writes = %d", err, fs.writeCount())
	}
	if !strings.Contains(stderr, "sent 100 of 180 writes before the failure") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestBackfillMachineForceWithNothingMissing(t *testing.T) {
	all := []map[string]any{storedTuple("u1", "member"), storedTuple("u1", "reader"), storedTuple("u2", "member"), storedTuple("u2", "reader")}
	for name, setup := range map[string]func(c *cli.CLI){
		"json":  func(c *cli.CLI) { c.JSON = true },
		"agent": func(c *cli.CLI) { c.JSON = true; c.Agent = agentmode.State{Enabled: true, Reason: "CLAUDECODE=1"} },
	} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t, all...)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
			mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
			c := fs.cli()
			setup(c)
			stdout, stderr, err := runBackfill(t, c, nil, mp, "--provider", "fake", "--force")
			if err != nil {
				t.Fatalf("err = %v\n%s", err, stderr)
			}
			if fs.writeCount() != 0 {
				t.Fatalf("wrote %d times with nothing missing", fs.writeCount())
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
				t.Fatal(err)
			}
			var got backfillResult
			_ = json.Unmarshal([]byte(stdout), &got)
			if string(raw["missing"]) != "[]" || got.Existing != 4 || got.Sent.Writes != 0 {
				t.Fatalf("document:\n%s", stdout)
			}
		})
	}
}

func TestBackfillEveryTupleChanged(t *testing.T) {
	configtest.Isolate(t)
	var stored []map[string]any
	for _, u := range []string{"u1", "u2"} {
		for _, r := range []string{"member", "reader"} {
			tp := storedTuple(u, r)
			tp["condition"] = map[string]any{"name": "in_office"}
			stored = append(stored, tp)
		}
	}
	fs := newFakeStore(t, stored...)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	c := fs.cli()
	c.NoInput = true
	_, stderr, err := runBackfill(t, c, strings.NewReader(""), mp, "--provider", "fake")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("wrote %d times", fs.writeCount())
	}
	for _, want := range []string{
		`4 tuples are in the store with a different condition; backfill leaves them, "ofga mappings drift" replaces them`,
		"nothing to write: 0 tuples already in store acme, 4 there with a different condition (Fake state as of 14:05:40 UTC)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
