package mapping

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	_ "modernc.org/sqlite"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// unitsOf is one unit per user, each with one event of type typ.
func unitsOf(typ string, users ...string) []fakeUnit {
	var us []fakeUnit
	for _, u := range users {
		us = append(us, fakeUnit{key: "org/" + u, events: []map[string]any{{"type": typ, "user": u, "org": "o1"}}})
	}
	return us
}

func memberUnits(users ...string) []fakeUnit {
	return unitsOf("snapshot.organization.member", users...)
}

// unitProvider reads units; failAfter > 0 makes the read fail after that many.
func unitProvider(units []fakeUnit, failAfter int, read *[]string) fakeProvider {
	res := fakeResult()
	res.Events = nil
	return fakeProvider{types: fakeTypes(), result: res, units: units, failAfter: failAfter, readUnits: read}
}

func (fs *fakeStore) writesText() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return strings.Join(fs.writes, "\n")
}

func (fs *fakeStore) cliFor(storeID string) *cli.CLI {
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: fs.srv.URL, StoreID: storeID})
	return cli.New(log.New(io.Discard), cfg, "test")
}

func TestResumeReadsOnlyUnfinishedUnits(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2", "u3", "u4")

	var first []string
	useFakeProvider(t, unitProvider(units, 2, &first))
	_, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...)
	if err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("a failed read wrote %d times", fs.writeCount())
	}

	var second []string
	useFakeProvider(t, unitProvider(units, 0, &second))
	stdout, stderr, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if want := []string{"org/u3", "org/u4"}; !slices.Equal(second, want) {
		t.Errorf("units read on resume = %v, want %v", second, want)
	}
	written := fs.writesText()
	for _, u := range []string{"u1", "u2", "u3", "u4"} {
		if !strings.Contains(written, "user:"+u) {
			t.Errorf("user:%s was not written:\n%s", u, written)
		}
	}
	found := false
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "tenant.example") && strings.HasSuffix(strings.TrimSpace(line), "(resumed)") {
			found = true
		}
	}
	if !found {
		t.Errorf("no snapshot line ends with (resumed):\n%s", stdout)
	}
	if exists(state) {
		t.Error("the finished run left its file")
	}
}

func TestResumeRefusals(t *testing.T) {
	const otherStore = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	type tc struct {
		name, reason string
		firstRun     bool // whether a first run makes the file
		first        []string
		types        []snapshot.Type
		mapping      string
		typ          string
		command      string
		resume       []string
		cliFor       func(fs *fakeStore) *cli.CLI
		source       string
		prep         func(t *testing.T, state string)
	}
	cases := []tc{
		{name: "missing file", reason: "there is no work file", resume: nil},
		{name: "another store", reason: "it was made for store " + "01ARZ3NDEKTSV4RRFFQ69G5FAV", firstRun: true,
			cliFor: func(fs *fakeStore) *cli.CLI { fs.altStore = otherStore; return fs.cliFor(otherStore) }},
		{name: "another tenant", reason: "it was made for tenant fake.example, not other.example", firstRun: true, source: "other.example"},
		{name: "another since", reason: `it was made with --since "2h", not "3h"`, firstRun: true,
			first: []string{"--since", "2h"}, resume: []string{"--since", "3h"},
			types: windowedTypes(), mapping: userSnapshotMapping, typ: "snapshot.user"},
		{name: "too old", reason: "it is older than 24 hours", firstRun: true, prep: func(t *testing.T, state string) {
			db, err := sql.Open("sqlite", state)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
			if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'created_at'`, old); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "another command", reason: "it was made for command backfill, not drift", firstRun: true, command: "drift"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			state := filepath.Join(t.TempDir(), "w.db")
			types, mapping, typ := c.types, c.mapping, c.typ
			if types == nil {
				types, mapping, typ = fakeTypes(), memberSnapshotMapping, "snapshot.organization.member"
			}
			units := unitsOf(typ, "u1", "u2", "u3")
			mk := func(failAfter int, source string) fakeProvider {
				p := unitProvider(units, failAfter, nil)
				p.types, p.source = types, source
				return p
			}
			if c.firstRun {
				useFakeProvider(t, mk(1, ""))
				_, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, mapping, state, append([]string{"--force"}, c.first...)...)...)
				if err == nil || !exists(state) {
					t.Fatalf("the first run should fail and keep its file: err = %v", err)
				}
			}
			if c.prep != nil {
				c.prep(t, state)
			}
			useFakeProvider(t, mk(0, c.source))
			cl := fs.cli()
			if c.cliFor != nil {
				cl = c.cliFor(fs)
			}
			command := "backfill"
			if c.command != "" {
				command = c.command
			}
			args := stateArgs(t, mapping, state, append([]string{"--force", "--resume"}, c.resume...)...)
			_, _, err := runSnapshotCmd(t, cl, nil, command, args...)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if clierr.Code(err) != clierr.CodeUsage {
				t.Errorf("code = %d, want a usage error: %v", clierr.Code(err), err)
			}
			if prefix := "cannot resume from " + state + ": "; !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("err = %q, want prefix %q and reason %q", err, prefix, c.reason)
			}
			if hint := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(hint, "run without --resume to start over") {
				t.Errorf("hint = %q", hint)
			}
			if c.firstRun && !exists(state) {
				t.Error("the refusal deleted the file")
			}
			if !c.firstRun && exists(state) {
				t.Error("a refusal created a file")
			}
			if fs.writeCount() != 0 {
				t.Errorf("a refused resume wrote %d times", fs.writeCount())
			}
		})
	}
}

func TestResumeWithAChangedMapping(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")

	useFakeProvider(t, unitProvider(units, 0, nil))
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	_, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...)
	if err == nil || !exists(state) {
		t.Fatalf("the first run should fail at the write and keep its file: err = %v", err)
	}
	fs.mu.Lock()
	fs.failWrite, fs.writes = "", nil
	fs.mu.Unlock()

	edited := strings.Replace(memberSnapshotMapping, "relation: reader", "relation: viewer", 1)
	edited += `  - name: organization.member.owner
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: owner
        object: "organization:{{ input.org }}"
`
	var read []string
	useFakeProvider(t, unitProvider(units, 0, &read))
	_, stderr, err := runBackfill(t, fs.cli(), nil, stateArgs(t, edited, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(read) != 0 {
		t.Errorf("the provider was read again for %v", read)
	}
	written := fs.writesText()
	for _, want := range []string{"viewer", "owner", "user:u2"} {
		if !strings.Contains(written, want) {
			t.Errorf("writes lack %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, "reader") {
		t.Errorf("the old mapping's tuples were written:\n%s", written)
	}
}

func TestResumeRefusesANewSnapshotType(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	types := append(fakeTypes(), snapshot.Type{Name: "snapshot.organization.connection", Mirrors: "organization.connection.added"})
	units := memberUnits("u1", "u2")
	mk := func(failAfter int) fakeProvider {
		p := unitProvider(units, failAfter, nil)
		p.types = types
		return p
	}
	useFakeProvider(t, mk(1))
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}
	edited := memberSnapshotMapping + `  - name: organization.connection.added
    when: input.type == "snapshot.organization.connection"
    tuples:
      - user: "user:{{ input.user }}"
        relation: connected
        object: "organization:o1"
`
	useFakeProvider(t, mk(0))
	_, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, edited, state, "--force", "--resume")...)
	if err == nil || !strings.Contains(err.Error(), "cannot resume from "+state+": ") || !strings.Contains(err.Error(), "snapshot.organization.connection") {
		t.Fatalf("err = %v", err)
	}
	if !exists(state) {
		t.Error("the refusal deleted the file")
	}
}

func TestResumeKeepsARelativeSince(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := unitsOf("snapshot.user", "u1", "u2")
	mk := func(failAfter int, got *time.Time) fakeProvider {
		p := unitProvider(units, failAfter, nil)
		p.types, p.gotSince = windowedTypes(), got
		return p
	}
	pinNow(t, 0)
	useFakeProvider(t, mk(1, nil))
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, userSnapshotMapping, state, "--since", "24h", "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}

	pinNow(t, time.Hour)
	var got time.Time
	useFakeProvider(t, mk(0, &got))
	_, stderr, err := runBackfill(t, fs.cli(), nil, stateArgs(t, userSnapshotMapping, state, "--since", "24h", "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if want := snapStart.Add(-24 * time.Hour); !got.Equal(want) {
		t.Errorf("provider since = %v, want %v", got, want)
	}
}

// removeStored drops the stored tuples from the fake store.
func (fs *fakeStore) removeStored() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.tuples = nil
}

func TestResumedDriftReusesItsStoreRead(t *testing.T) {
	configtest.Isolate(t)
	// u1 is stored without the mapping's condition (changed); u2 is not stored
	// (missing).
	fs := newFakeStore(t, member("u1"))
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 0, nil))

	// The missing write goes through; replacing u1 fails on its delete.
	fs.failAt = map[int]bool{2: true}
	c := fs.cli()
	c.JSON = true
	_, _, err := runDrift(t, c, nil, stateArgs(t, driftConditionMapping, state, "--force")...)
	if err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites, firstReads := fs.writeCount(), len(fs.readRequests())
	if firstWrites != 3 || firstReads == 0 {
		t.Fatalf("first run: %d writes, %d reads", firstWrites, firstReads)
	}

	// A fresh store read would no longer see u1, and plan a different write.
	fs.removeStored()
	fs.mu.Lock()
	fs.failAt = nil
	fs.mu.Unlock()
	c = fs.cli()
	c.JSON = true
	stdout, stderr, err := runDrift(t, c, nil, stateArgs(t, driftConditionMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if n := len(fs.readRequests()); n != firstReads {
		t.Errorf("the resumed drift read the store %d more times", n-firstReads)
	}
	// Only the replacement of u1 is sent: its delete and its write.
	if n := fs.writeCount() - firstWrites; n != 2 {
		t.Errorf("the resumed drift sent %d writes, want 2", n)
	}
	if rest := strings.Join(strings.Split(fs.writesText(), "\n")[firstWrites:], "\n"); strings.Contains(rest, "u2") {
		t.Errorf("the batch already sent was sent again:\n%s", rest)
	}
	var doc struct {
		Sent struct{ Writes, Deletes int } `json:"sent"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	// One missing write from the first run, one replacement write now.
	if doc.Sent.Writes != 2 || doc.Sent.Deletes != 1 {
		t.Errorf("sent = %+v, want 2 writes and 1 delete", doc.Sent)
	}
}

func TestResumedDriftAfterAProviderFailureKeepsTheStoreItRead(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 1, nil))
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, driftMapping, state, "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}
	reads := len(fs.readRequests())
	if reads == 0 {
		t.Fatal("the first run did not read the store")
	}

	fs.removeStored()
	var read []string
	useFakeProvider(t, unitProvider(units, 0, &read))
	c := fs.cli()
	c.JSON = true
	stdout, _, _ := runDrift(t, c, nil, stateArgs(t, driftMapping, state, "--dry-run", "--resume")...)
	if n := len(fs.readRequests()); n != reads {
		t.Errorf("the resumed drift read the store %d more times", n-reads)
	}
	if !slices.Equal(read, []string{"org/u2"}) {
		t.Errorf("units read = %v", read)
	}
	var doc struct {
		Stale []tupleOut `json:"stale"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(doc.Stale) != 1 || doc.Stale[0].User != "user:gone" {
		t.Errorf("stale = %+v, want the tuple the saved store read held", doc.Stale)
	}
}

func TestResumedBackfillReadsTheStoreAgain(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2", "u3", "u4")
	useFakeProvider(t, unitProvider(units, 2, nil))
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}

	// What an earlier run got through is in the store now.
	fs.mu.Lock()
	for _, u := range []string{"u1", "u2"} {
		for _, rel := range []string{"member", "reader"} {
			fs.tuples = append(fs.tuples, map[string]any{"user": "user:" + u, "relation": rel, "object": "organization:o1"})
		}
	}
	var log []string
	fs.log = &log
	fs.mu.Unlock()
	p := unitProvider(units, 0, nil)
	p.log = &log
	useFakeProvider(t, p)
	_, stderr, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if len(log) < 2 || log[0] != "provider read" || !slices.Contains(log, "store read") {
		t.Errorf("order = %v, want the provider read, then store reads", log)
	}
	written := fs.writesText()
	for _, u := range []string{"u3", "u4"} {
		if !strings.Contains(written, "user:"+u) {
			t.Errorf("user:%s was not written:\n%s", u, written)
		}
	}
	for _, u := range []string{"u1", "u2"} {
		if strings.Contains(written, "user:"+u) {
			t.Errorf("user:%s was written again:\n%s", u, written)
		}
	}
}

func TestResumeProgressCard(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2", "u3", "u4")
	useFakeProvider(t, unitProvider(units, 2, nil))
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}

	var read []string
	useFakeProvider(t, unitProvider(units, 0, &read))
	w, err := runTogether(t, fs.cli(), nil, "backfill", stateArgs(t, memberSnapshotMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, w.text())
	}
	first, _, _ := strings.Cut(strings.TrimLeft(w.text(), "\n"), "\n")
	if want := "resuming from " + state; !strings.Contains(first, want) {
		t.Errorf("first line = %q, want it to say %q", first, want)
	}
	if !slices.Equal(read, []string{"org/u3", "org/u4"}) {
		t.Errorf("units read = %v", read)
	}
	if _, err := os.Stat(state); err == nil {
		t.Error("the finished run left its file")
	}
}
