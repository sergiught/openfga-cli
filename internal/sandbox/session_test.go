package sandbox

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

func startSession(t *testing.T, o Options) *Session {
	t.Helper()
	s, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func storeIDs(t *testing.T, s *Session) map[string]string {
	t.Helper()
	c, err := openfga.NewClient(s.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Stores.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, st := range resp.Stores {
		ids[st.Name] = st.ID
	}
	if len(ids) != len(resp.Stores) {
		t.Fatalf("duplicate store names: len = %d, want %d", len(ids), len(resp.Stores))
	}
	return ids
}

func storeClient(t *testing.T, s *Session, name string) *openfga.Client {
	t.Helper()
	id, ok := storeIDs(t, s)[name]
	if !ok {
		t.Fatalf("store %s not found", name)
	}
	c, err := openfga.NewClient(s.Endpoint(), openfga.WithStoreID(id))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tupleCount(t *testing.T, c *openfga.Client) int {
	t.Helper()
	r, err := c.Tuples.Read(context.Background(), &openfga.ReadRequest{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(r.Tuples)
}

func writeTuple(t *testing.T, c *openfga.Client, user, rel, obj string) {
	t.Helper()
	err := c.Tuples.Write(context.Background(), &openfga.WriteRequest{
		Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{openfga.NewTupleKey(user, rel, obj)}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSession_FreshHasSamples(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})

	ids := storeIDs(t, s)
	if len(ids) != 7 {
		t.Fatalf("len = %d, want %d", len(ids), 7)
	}
	if got := len(s.Workspaces()); got != 7 {
		t.Fatalf("len = %d, want %d", got, 7)
	}
	for _, name := range SampleNames() {
		if _, ok := ids[name]; !ok {
			t.Fatalf("missing %q", name)
		}
		if _, ok := s.Workspaces()[name]; !ok {
			t.Fatalf("missing %q", name)
		}
		c, err := openfga.NewClient(s.Endpoint(), openfga.WithStoreID(ids[name]))
		if err != nil {
			t.Fatal(err)
		}
		ms, err := c.AuthorizationModels.List(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms.AuthorizationModels) == 0 {
			t.Fatal("expected non-empty")
		}
		if tupleCount(t, c) == 0 {
			t.Fatal("expected non-zero")
		}
	}
	if s.Dirty() {
		t.Fatal("seeding is not a user change")
	}
}

func TestSession_FreshNoChangesNotDirty(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	if s.Dirty() {
		t.Fatal("expected false")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saved); err == nil {
		t.Fatalf("%s exists, want it absent", saved)
	}
}

func TestSession_ReadsDoNotDirty(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	ctx := context.Background()
	c := storeClient(t, s, "github")

	r, err := c.Tuples.Read(ctx, &openfga.ReadRequest{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tuples) == 0 {
		t.Fatal("expected non-empty")
	}
	k := r.Tuples[0].Key

	_, err = c.Relationships.Check(ctx, openfga.NewCheckRequest(k.User, k.Relation, k.Object))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Relationships.Expand(ctx, &openfga.ExpandRequest{TupleKey: openfga.CheckRequestTupleKey{Relation: k.Relation, Object: k.Object}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Relationships.ListObjects(ctx, &openfga.ListObjectsRequest{Type: "repo", Relation: "reader", User: "user:anne"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Relationships.ListUsers(ctx, &openfga.ListUsersRequest{
		Object:      openfga.FGAObjectRelation{Object: k.Object},
		Relation:    k.Relation,
		UserFilters: []openfga.UserTypeFilter{{Type: "user"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if s.Dirty() {
		t.Fatal("expected false")
	}
}

func TestSession_WriteDirties(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	writeTuple(t, storeClient(t, s, "github"), "user:newbie", "reader", "repo:openfga/openfga")
	if !s.Dirty() {
		t.Fatal("expected true")
	}
}

func TestSession_SaveThenRestore(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "sub", "p.db")
	s := startSession(t, Options{SavedPath: saved})
	c := storeClient(t, s, "github")
	before := tupleCount(t, c)
	writeTuple(t, c, "user:newbie", "reader", "repo:openfga/openfga")
	scratch := s.scratch
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if got := s.Close(); got != nil {
		t.Fatalf("Close after Save is a no-op: %v", got)
	}
	if _, err := os.Stat(scratch); err == nil {
		t.Fatalf("%s exists, want it absent", scratch)
	}

	fi, err := os.Stat(saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != os.FileMode(0o600) {
		t.Fatalf("got %v, want %v", got, os.FileMode(0o600))
	}
	if _, err := os.Stat(saved + "-wal"); err == nil {
		t.Fatalf("%s exists, want it absent", saved+"-wal")
	}
	entries, err := os.ReadDir(filepath.Dir(saved))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("no staged temp files left: len = %d, want %d", len(entries), 1)
	}

	s2 := startSession(t, Options{SavedPath: saved})
	if s2.Dirty() {
		t.Fatal("expected false")
	}
	if got := len(storeIDs(t, s2)); got != 7 {
		t.Fatalf("no duplicate sample stores: len = %d, want %d", got, 7)
	}
	if got := tupleCount(t, storeClient(t, s2, "github")); got != before+1 {
		t.Fatalf("got %v, want %v", got, before+1)
	}
	if got := len(s2.Workspaces()); got != 7 {
		t.Fatalf("len = %d, want %d", got, 7)
	}
}

func TestSession_ResetIgnoresSaved(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	c := storeClient(t, s, "github")
	before := tupleCount(t, c)
	writeTuple(t, c, "user:newbie", "reader", "repo:openfga/openfga")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2 := startSession(t, Options{SavedPath: saved, Reset: true})
	if got := tupleCount(t, storeClient(t, s2, "github")); got != before {
		t.Fatalf("got %v, want %v", got, before)
	}
	if s2.Dirty() {
		t.Fatal("expected false")
	}
}

func TestSession_DiscardLeavesSavedFile(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}

	for _, reset := range []bool{false, true} {
		s2 := startSession(t, Options{SavedPath: saved, Reset: reset})
		writeTuple(t, storeClient(t, s2, "github"), "user:newbie", "reader", "repo:openfga/openfga")
		if !s2.Dirty() {
			t.Fatal("expected true")
		}
		if err := s2.Close(); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(saved)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("reset=%v: saved file changed by a discarded session", reset)
		}
		entries, err := os.ReadDir(filepath.Dir(saved))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("len = %d, want %d", len(entries), 1)
		}
	}
}

func TestStart_SavedTooNewLeavesFileUntouched(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(saved)
	if err != nil {
		t.Fatal(err)
	}
	ds.Close()
	db, err := sql.Open("sqlite", saved)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999999, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := hashFiles(t, saved)

	_, err = Start(context.Background(), Options{SavedPath: saved})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("error = %v, want %v", err, ErrSchemaTooNew)
	}
	if !strings.Contains(err.Error(), "ofga playground --reset") {
		t.Fatalf("%q does not contain %q", err.Error(), "ofga playground --reset")
	}
	if got := hashFiles(t, saved); got != before {
		t.Fatalf("got %v, want %v", got, before)
	}
}

func TestStart_CorruptSaved(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	if err := os.WriteFile(saved, []byte("this is definitely not a sqlite database, just garbage bytes padding padding padding"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Start(context.Background(), Options{SavedPath: saved})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ofga playground --reset") {
		t.Fatalf("%q does not contain %q", err.Error(), "ofga playground --reset")
	}
	b, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "definitely not a sqlite") {
		t.Fatalf("%q does not contain %q", string(b), "definitely not a sqlite")
	}
}

func TestSession_CloseRemovesScratch(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	if _, err := os.Stat(s.scratch); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.scratch); err == nil {
		t.Fatalf("%s exists, want it absent", s.scratch)
	}
	if got := s.Close(); got != nil {
		t.Fatalf("idempotent: %v", got)
	}
}

func TestDefaultPath_EnvOverride(t *testing.T) {
	t.Setenv(DBEnv, "/tmp/custom/playground.db")
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != "/tmp/custom/playground.db" {
		t.Fatalf("got %v, want %v", p, "/tmp/custom/playground.db")
	}

	t.Setenv(DBEnv, "")
	p, err = DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(p); got != "playground.db" {
		t.Fatalf("got %v, want %v", got, "playground.db")
	}
}

func TestSession_FailedSaveKeepsScratch(t *testing.T) {
	configtest.Isolate(t)
	blocker := filepath.Join(t.TempDir(), "dir")
	s := startSession(t, Options{SavedPath: filepath.Join(blocker, "playground.db")})
	writeTuple(t, storeClient(t, s, "github"), "user:zed", "reader", "repo:acme/foo")
	// The saved path's parent becomes a regular file, so the data dir cannot be created.
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	saveErr := s.Save()
	if saveErr == nil {
		t.Fatal("Save succeeded, want error")
	}
	scratchDB := s.scratchDB
	if !strings.Contains(saveErr.Error(), scratchDB) {
		t.Errorf("Save error %q does not name the scratch database %s", saveErr, scratchDB)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(scratchDB); err != nil {
		t.Fatalf("scratch database removed after failed Save: %v", err)
	}
	db, err := OpenDatastore(scratchDB)
	if err != nil {
		t.Fatalf("scratch database not openable: %v", err)
	}
	db.Close()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(scratchDB)) })
}
