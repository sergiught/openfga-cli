package sandbox

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/stretchr/testify/require"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

func startSession(t *testing.T, o Options) *Session {
	t.Helper()
	s, err := Start(context.Background(), o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func storeIDs(t *testing.T, s *Session) map[string]string {
	t.Helper()
	c, err := openfga.NewClient(s.Endpoint())
	require.NoError(t, err)
	resp, err := c.Stores.List(context.Background(), nil)
	require.NoError(t, err)
	ids := map[string]string{}
	for _, st := range resp.Stores {
		ids[st.Name] = st.ID
	}
	require.Len(t, ids, len(resp.Stores), "duplicate store names")
	return ids
}

func storeClient(t *testing.T, s *Session, name string) *openfga.Client {
	t.Helper()
	id, ok := storeIDs(t, s)[name]
	require.True(t, ok, "store %s", name)
	c, err := openfga.NewClient(s.Endpoint(), openfga.WithStoreID(id))
	require.NoError(t, err)
	return c
}

func tupleCount(t *testing.T, c *openfga.Client) int {
	t.Helper()
	r, err := c.Tuples.Read(context.Background(), &openfga.ReadRequest{PageSize: 100})
	require.NoError(t, err)
	return len(r.Tuples)
}

func writeTuple(t *testing.T, c *openfga.Client, user, rel, obj string) {
	t.Helper()
	err := c.Tuples.Write(context.Background(), &openfga.WriteRequest{
		Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{openfga.NewTupleKey(user, rel, obj)}},
	})
	require.NoError(t, err)
}

func TestSession_FreshHasSamples(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})

	ids := storeIDs(t, s)
	require.Len(t, ids, 7)
	require.Len(t, s.Workspaces(), 7)
	for _, name := range SampleNames() {
		require.Contains(t, ids, name)
		require.Contains(t, s.Workspaces(), name)
		c, err := openfga.NewClient(s.Endpoint(), openfga.WithStoreID(ids[name]))
		require.NoError(t, err)
		ms, err := c.AuthorizationModels.List(context.Background(), nil)
		require.NoError(t, err)
		require.NotEmpty(t, ms.AuthorizationModels, name)
		require.NotZero(t, tupleCount(t, c), name)
	}
	require.False(t, s.Dirty(), "seeding is not a user change")
}

func TestSession_FreshNoChangesNotDirty(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	require.False(t, s.Dirty())
	require.NoError(t, s.Close())
	require.NoFileExists(t, saved)
}

func TestSession_ReadsDoNotDirty(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	ctx := context.Background()
	c := storeClient(t, s, "github")

	r, err := c.Tuples.Read(ctx, &openfga.ReadRequest{PageSize: 1})
	require.NoError(t, err)
	require.NotEmpty(t, r.Tuples)
	k := r.Tuples[0].Key

	_, err = c.Relationships.Check(ctx, openfga.NewCheckRequest(k.User, k.Relation, k.Object))
	require.NoError(t, err)
	_, err = c.Relationships.Expand(ctx, &openfga.ExpandRequest{TupleKey: openfga.CheckRequestTupleKey{Relation: k.Relation, Object: k.Object}})
	require.NoError(t, err)
	_, err = c.Relationships.ListObjects(ctx, &openfga.ListObjectsRequest{Type: "repo", Relation: "reader", User: "user:anne"})
	require.NoError(t, err)
	_, err = c.Relationships.ListUsers(ctx, &openfga.ListUsersRequest{
		Object:      openfga.FGAObjectRelation{Object: k.Object},
		Relation:    k.Relation,
		UserFilters: []openfga.UserTypeFilter{{Type: "user"}},
	})
	require.NoError(t, err)

	require.False(t, s.Dirty())
}

func TestSession_WriteDirties(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	writeTuple(t, storeClient(t, s, "github"), "user:newbie", "reader", "repo:openfga/openfga")
	require.True(t, s.Dirty())
}

func TestSession_SaveThenRestore(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "sub", "p.db")
	s := startSession(t, Options{SavedPath: saved})
	c := storeClient(t, s, "github")
	before := tupleCount(t, c)
	writeTuple(t, c, "user:newbie", "reader", "repo:openfga/openfga")
	scratch := s.scratch
	require.NoError(t, s.Save())
	require.NoError(t, s.Close(), "Close after Save is a no-op")
	require.NoDirExists(t, scratch)

	fi, err := os.Stat(saved)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	require.NoFileExists(t, saved+"-wal")
	entries, err := os.ReadDir(filepath.Dir(saved))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no staged temp files left")

	s2 := startSession(t, Options{SavedPath: saved})
	require.False(t, s2.Dirty())
	require.Len(t, storeIDs(t, s2), 7, "no duplicate sample stores")
	require.Equal(t, before+1, tupleCount(t, storeClient(t, s2, "github")))
	require.Len(t, s2.Workspaces(), 7)
}

func TestSession_ResetIgnoresSaved(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	c := storeClient(t, s, "github")
	before := tupleCount(t, c)
	writeTuple(t, c, "user:newbie", "reader", "repo:openfga/openfga")
	require.NoError(t, s.Save())

	s2 := startSession(t, Options{SavedPath: saved, Reset: true})
	require.Equal(t, before, tupleCount(t, storeClient(t, s2, "github")))
	require.False(t, s2.Dirty())
}

func TestSession_DiscardLeavesSavedFile(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	s := startSession(t, Options{SavedPath: saved})
	require.NoError(t, s.Save())
	want, err := os.ReadFile(saved)
	require.NoError(t, err)

	for _, reset := range []bool{false, true} {
		s2 := startSession(t, Options{SavedPath: saved, Reset: reset})
		writeTuple(t, storeClient(t, s2, "github"), "user:newbie", "reader", "repo:openfga/openfga")
		require.True(t, s2.Dirty())
		require.NoError(t, s2.Close())

		got, err := os.ReadFile(saved)
		require.NoError(t, err)
		require.Equal(t, want, got, "reset=%v", reset)
		entries, err := os.ReadDir(filepath.Dir(saved))
		require.NoError(t, err)
		require.Len(t, entries, 1)
	}
}

func TestStart_SavedTooNewLeavesFileUntouched(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(saved)
	require.NoError(t, err)
	ds.Close()
	db, err := sql.Open("sqlite", saved)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999999, 1)`)
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	before := hashFiles(t, saved)

	_, err = Start(context.Background(), Options{SavedPath: saved})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSchemaTooNew)
	require.Contains(t, err.Error(), "ofga playground --reset")
	require.Equal(t, before, hashFiles(t, saved))
}

func TestStart_CorruptSaved(t *testing.T) {
	configtest.Isolate(t)
	saved := filepath.Join(t.TempDir(), "p.db")
	require.NoError(t, os.WriteFile(saved, []byte("this is definitely not a sqlite database, just garbage bytes padding padding padding"), 0o600))

	_, err := Start(context.Background(), Options{SavedPath: saved})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ofga playground --reset")
	b, err := os.ReadFile(saved)
	require.NoError(t, err)
	require.Contains(t, string(b), "definitely not a sqlite")
}

func TestSession_CloseRemovesScratch(t *testing.T) {
	configtest.Isolate(t)
	s := startSession(t, Options{SavedPath: filepath.Join(t.TempDir(), "p.db")})
	require.DirExists(t, s.scratch)
	require.NoError(t, s.Close())
	require.NoDirExists(t, s.scratch)
	require.NoError(t, s.Close(), "idempotent")
}

func TestDefaultPath_EnvOverride(t *testing.T) {
	t.Setenv(DBEnv, "/tmp/custom/playground.db")
	p, err := DefaultPath()
	require.NoError(t, err)
	require.Equal(t, "/tmp/custom/playground.db", p)

	t.Setenv(DBEnv, "")
	p, err = DefaultPath()
	require.NoError(t, err)
	require.Equal(t, "playground.db", filepath.Base(p))
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
