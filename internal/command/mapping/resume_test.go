package mapping

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/openfga/mapper/language"
	_ "modernc.org/sqlite"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
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

// smallPages makes a page of the diff one row for the test.
func smallPages(t *testing.T) {
	t.Helper()
	old := writePage
	writePage = 1
	t.Cleanup(func() { writePage = old })
}

// writtenAfter is the text of the writes the fake store took after the first n.
func (fs *fakeStore) writtenAfter(n int) string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return strings.Join(fs.writes[n:], "\n")
}

func TestResumedBackfillSendsASentTupleTheStoreLost(t *testing.T) {
	configtest.Isolate(t)
	smallPages(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 0, nil))

	// The first page goes through; the second does not.
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites := fs.writeCount()

	// The store holds nothing: the tuple the first run sent is gone again.
	fs.mu.Lock()
	fs.failAt = nil
	fs.mu.Unlock()
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runBackfill(t, c, nil, stateArgs(t, memberSnapshotMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	written := fs.writtenAfter(firstWrites)
	for _, u := range []string{"u1", "u2"} {
		for _, rel := range []string{"member", "reader"} {
			if !strings.Contains(written, `"user":"user:`+u+`"`) || !strings.Contains(written, `"relation":"`+rel+`"`) {
				t.Errorf("%s of %s was not written after the resume:\n%s", rel, u, written)
			}
		}
	}
	if n := fs.writeCount() - firstWrites; n != 4 {
		t.Errorf("the resumed run sent %d writes, want 4 (one per missing tuple)", n)
	}
	var doc struct {
		Sent struct{ Writes int } `json:"sent"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if doc.Sent.Writes != 5 {
		t.Errorf("sent.writes = %d, want 5 (one from the first run, four now)", doc.Sent.Writes)
	}
}

func TestResumedUsersOnlyDriftSendsASentTupleTheStoreLost(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	smallPages(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := unitsOf("snapshot.user", "u1", "u2")
	mk := func() fakeProvider {
		p := unitProvider(units, 0, nil)
		p.types = windowedTypes()
		return p
	}
	useFakeProvider(t, mk())
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, userSnapshotMapping, state, "--since", "2h", "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites := fs.writeCount()

	fs.mu.Lock()
	fs.failAt = nil
	fs.mu.Unlock()
	useFakeProvider(t, mk())
	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runDrift(t, c, nil, stateArgs(t, userSnapshotMapping, state, "--since", "2h", "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	written := fs.writtenAfter(firstWrites)
	for _, u := range []string{"u1", "u2"} {
		if !strings.Contains(written, `"user":"user:`+u+`"`) {
			t.Errorf("user:%s was not written after the resume:\n%s", u, written)
		}
	}
	var doc struct {
		Sent struct{ Writes int } `json:"sent"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if doc.Sent.Writes != 3 {
		t.Errorf("sent.writes = %d, want 3 (one from the first run, two now)", doc.Sent.Writes)
	}
}

func TestPrepareResumeKeepsOnlyAStoreReadTakenBeforeTheProvider(t *testing.T) {
	for _, tc := range []struct {
		name           string
		flag           bool
		beforeProvider bool
		units          bool
		kept, refused  bool
	}{
		{"read before the provider", true, true, true, true, false},
		{"read after the provider", false, true, true, false, true},
		{"read after the provider, nothing read yet", false, true, false, false, false},
		{"this run reads after the provider", true, false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := workfile.Create(filepath.Join(t.TempDir(), "w.db"), workfile.Meta{
				Command: "drift", CreatedAt: now(), Types: []string{"snapshot.organization.member"}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = wf.Close() }()
			ctx := context.Background()
			if tc.units {
				ev := []map[string]any{{"type": "snapshot.organization.member", "user": "a", "org": "o"}}
				if err := wf.Save(snapshot.Unit{Key: "org/a"}, ev, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := wf.AddStore(ctx, []language.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}); err != nil {
				t.Fatal(err)
			}
			if err := wf.MarkStoreRead(now(), 1, 0); err != nil {
				t.Fatal(err)
			}
			if tc.flag {
				if err := wf.Remember(storeBeforeProvider, "1"); err != nil {
					t.Fatal(err)
				}
			}
			_, _, kept, err := prepareResume(ctx, wf, tc.beforeProvider, nil)
			var refusal *workfile.RefusalError
			if got := errors.As(err, &refusal); got != tc.refused {
				t.Fatalf("refused = %v, want %v (err = %v)", got, tc.refused, err)
			}
			if tc.refused {
				if refusal.Reason != "the earlier run read the store after the provider; this run must read it before" {
					t.Errorf("reason = %q", refusal.Reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, ok := wf.StoreRead(); kept != tc.kept || ok != tc.kept {
				t.Errorf("kept = %v, store read present = %v, want %v", kept, ok, tc.kept)
			}
		})
	}
}

// writtenKeys is the tuples a /write body writes.
func writtenKeys(body string) []map[string]any {
	var w struct {
		Writes struct {
			TupleKeys []map[string]any `json:"tuple_keys"`
		} `json:"writes"`
	}
	_ = json.Unmarshal([]byte(body), &w)
	return w.Writes.TupleKeys
}

func (fs *fakeStore) bodiesAfter(n int) []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.writes[n:]...)
}

const conditionedMemberMapping = `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
        condition: %s
`

func TestResumedDriftWritesTheEditedConditionOfATupleItAlreadySent(t *testing.T) {
	configtest.Isolate(t)
	smallPages(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 0, nil))
	mapA := strings.Replace(conditionedMemberMapping, "%s", "cond_a", 1)
	mapB := strings.Replace(conditionedMemberMapping, "%s", "cond_b", 1)

	// The first page goes through with condition A; the second does not.
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, mapA, state, "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites := fs.writeCount()
	fs.mu.Lock()
	fs.failAt = nil
	fs.mu.Unlock()

	if _, stderr, err := runDrift(t, fs.cli(), nil, stateArgs(t, mapB, state, "--force", "--resume")...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	written := strings.Join(fs.bodiesAfter(firstWrites), "\n")
	for _, u := range []string{"u1", "u2"} {
		if !strings.Contains(written, `"user":"user:`+u+`"`) {
			t.Errorf("user:%s not written after the resume:\n%s", u, written)
		}
	}
	if strings.Contains(written, "cond_a") || !strings.Contains(written, `"name":"cond_b"`) {
		t.Errorf("the resume must write condition B only:\n%s", written)
	}
}

func TestResumedDriftWritesATupleItDeletedAsStale(t *testing.T) {
	configtest.Isolate(t)
	smallPages(t)
	fs := newFakeStore(t, member("u1"), member("gone1"), member("gone2"))
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "gone1", "gone2")
	useFakeProvider(t, unitProvider(units, 0, nil))
	only := func(cond string) string {
		return `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"` + cond + `
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
`
	}
	mapA := only(` && input.user == "u1"`)
	mapB := only("")

	// The first stale page is deleted; the second fails.
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, mapA, state, "--prune", "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites := fs.writeCount()
	fs.mu.Lock()
	fs.failAt = nil
	fs.mu.Unlock()

	if _, stderr, err := runDrift(t, fs.cli(), nil, stateArgs(t, mapB, state, "--prune", "--force", "--resume")...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var wrote []string
	for _, b := range fs.bodiesAfter(firstWrites) {
		for _, k := range writtenKeys(b) {
			wrote = append(wrote, k["user"].(string))
		}
	}
	if !slices.Equal(wrote, []string{"user:gone1"}) {
		t.Errorf("written after the resume = %v, want only user:gone1", wrote)
	}
}

func TestResumedDriftDoesNotCountWhatItAlreadySentAsMissing(t *testing.T) {
	configtest.Isolate(t)
	smallPages(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 0, nil))
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, driftConditionMapping, state, "--force")...); err == nil {
		t.Fatal("the first run should fail")
	}
	c := fs.cli()
	c.JSON = true
	stdout, _, err := runDrift(t, c, nil, stateArgs(t, driftConditionMapping, state, "--dry-run", "--resume")...)
	if err == nil || !strings.Contains(clierr.NewEnvelope(err).Error.Message, "drift remains: 1 missing, 0 changed, 0 stale") {
		t.Fatalf("err = %v (%s), want only the tuple the first run did not send", err, clierr.NewEnvelope(err).Error.Message)
	}
	var doc struct {
		Missing []tupleOut `json:"missing"`
		Sent    struct{ Writes int }
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(doc.Missing) != 1 || doc.Sent.Writes != 1 {
		t.Errorf("missing = %d, sent.writes = %d; want 1 and 1", len(doc.Missing), doc.Sent.Writes)
	}
}

// flipMapping is a --since mapping whose rule on a windowed type either
// templates its relation (nothing is owned, so the store is read after the
// provider) or does not (the other rule's shape is owned, so it is read first).
func flipMapping(templated bool) string {
	rel := "member"
	if templated {
		rel = `"{{ input.rel }}"`
	}
	return `version: "1"
rules:
  - name: user.created
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user }}"
        relation: ` + rel + `
        object: "organization:o1"
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: reader
        object: "organization:{{ input.org }}"
`
}

func TestResumedDriftRefusesWhenItNoLongerCanReuseItsStoreRead(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 0)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := unitsOf("snapshot.user", "u1", "u2")
	mk := func(failAfter int) fakeProvider {
		p := unitProvider(units, failAfter, nil)
		p.types = windowedTypes()
		return p
	}

	// Users only: the store is read after the provider, so a failed provider
	// read leaves a finished unit and no store read.
	useFakeProvider(t, mk(1))
	if stdout1, stderr1, err := runDrift(t, fs.cli(), nil, stateArgs(t, flipMapping(true), state, "--since", "2h", "--force")...); err == nil || !exists(state) {
		t.Fatalf("first run: err = %v (%s), exists = %v\n%s%s", err, clierr.NewEnvelope(err).Error.Message, exists(state), stdout1, stderr1)
	}
	reads := len(fs.readRequests())

	// The edit makes another shape owned: the store would now be read before
	// the provider, and could not be paired with the unit already saved.
	useFakeProvider(t, mk(0))
	_, stderr, err := runDrift(t, fs.cli(), nil, stateArgs(t, flipMapping(false), state, "--since", "2h", "--force", "--resume")...)
	if strings.Contains(stderr, "--resume to continue") {
		t.Errorf("the refusal also says the work file was kept for a resume:\n%s", stderr)
	}
	if err == nil || clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d, err = %v; want a usage error", clierr.Code(err), err)
	}
	if want := "cannot resume from " + state + ": the earlier run read the store after the provider; this run must read it before"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to contain %q", err, want)
	}
	if hint := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(hint, "run without --resume to start over") || strings.Contains(hint, "--resume to continue") {
		t.Errorf("hint = %q", hint)
	}
	if fs.writeCount() != 0 || len(fs.readRequests()) != reads {
		t.Errorf("the refusal touched the store: %d writes, %d more reads", fs.writeCount(), len(fs.readRequests())-reads)
	}
	if !exists(state) {
		t.Error("the refusal deleted the work file")
	}
}

func TestDriftNotesThatItReadTheStoreBeforeTheProvider(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	state := filepath.Join(t.TempDir(), "w.db")
	useFakeProvider(t, unitProvider(memberUnits("u1", "u2"), 1, nil))
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, driftMapping, state, "--force")...); err == nil || !exists(state) {
		t.Fatalf("the run should fail and keep its file: err = %v", err)
	}
	wf, err := workfile.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wf.Close() }()
	if got := wf.Recall(storeBeforeProvider); got != "1" {
		t.Errorf("%s = %q, want 1", storeBeforeProvider, got)
	}
}

func TestResumedBackfillWritesEachTupleOnceAcrossPages(t *testing.T) {
	configtest.Isolate(t)
	old := writePage
	writePage = 2
	t.Cleanup(func() { writePage = old })
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	useFakeProvider(t, unitProvider(memberUnits("u1", "u2", "u3"), 0, nil))

	// Six tuples in three pages: the first goes through, the second does not.
	fs.failAt = map[int]bool{2: true}
	if _, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, memberSnapshotMapping, state, "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	firstWrites := fs.writeCount()
	if firstWrites != 2 {
		t.Fatalf("first run sent %d requests, want 2", firstWrites)
	}
	// The store now holds what the accepted request carried.
	fs.mu.Lock()
	fs.tuples = append(fs.tuples, writtenKeys(fs.writes[0])...)
	stored := len(fs.tuples)
	fs.failAt = nil
	fs.mu.Unlock()
	if stored != 2 {
		t.Fatalf("the first accepted request carried %d tuples, want 2", stored)
	}

	c := fs.cli()
	c.JSON = true
	stdout, stderr, err := runBackfill(t, c, nil, stateArgs(t, memberSnapshotMapping, state, "--force", "--resume")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	// Every tuple went through exactly once: the accepted request of the first
	// run, then the resumed run's requests (the rejected one is not counted).
	seen := map[string]int{}
	fs.mu.Lock()
	accepted := append([]string{fs.writes[0]}, fs.writes[2:]...)
	fs.mu.Unlock()
	for _, b := range accepted {
		for _, k := range writtenKeys(b) {
			seen[k["user"].(string)+" "+k["relation"].(string)]++
		}
	}
	if len(seen) != 6 {
		t.Errorf("%d distinct tuples written, want 6: %v", len(seen), seen)
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s written %d times", k, n)
		}
	}
	if n := fs.writeCount() - firstWrites; n != 2 {
		t.Errorf("the resumed run sent %d requests, want 2", n)
	}
	var doc struct {
		Missing []tupleOut `json:"missing"`
		Sent    struct{ Writes int }
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(doc.Missing) != 4 || doc.Sent.Writes != 6 {
		t.Errorf("missing = %d, sent.writes = %d; want 4 and 6 (two from the first run, four now)", len(doc.Missing), doc.Sent.Writes)
	}
}

func TestResumedDriftRefusesAShapeTheSavedStoreReadDidNotCover(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	state := filepath.Join(t.TempDir(), "w.db")
	units := memberUnits("u1", "u2")
	useFakeProvider(t, unitProvider(units, 1, nil))
	if _, _, err := runDrift(t, fs.cli(), nil, stateArgs(t, driftMapping, state, "--force")...); err == nil || !exists(state) {
		t.Fatalf("the first run should fail and keep its file: err = %v", err)
	}
	reads := len(fs.readRequests())
	wider := driftMapping + `  - name: extra
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: reader
        object: "organization:{{ input.org }}"
`
	useFakeProvider(t, unitProvider(units, 0, nil))
	_, stderr, err := runDrift(t, fs.cli(), nil, stateArgs(t, wider, state, "--dry-run", "--resume")...)
	if err == nil || clierr.Code(err) != clierr.CodeUsage ||
		!strings.Contains(err.Error(), "the mapping now writes user reader organization that the earlier store read did not cover") {
		t.Fatalf("code = %d, err = %v\n%s", clierr.Code(err), err, stderr)
	}
	if strings.Contains(stderr, "--resume to continue") || !exists(state) || len(fs.readRequests()) != reads {
		t.Errorf("the refusal kept the wrong state:\n%s", stderr)
	}

	// A mapping that drops a shape still resumes.
	narrower := `version: "1"
rules:
  - name: organization.member.added
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "organization:{{ input.org }}"
`
	useFakeProvider(t, unitProvider(units, 0, nil))
	if _, stderr, err := runDrift(t, fs.cli(), nil, stateArgs(t, narrower, state, "--dry-run", "--resume")...); err != nil && clierr.Code(err) == clierr.CodeUsage {
		t.Fatalf("a narrower mapping was refused: %v\n%s", err, stderr)
	}
}
