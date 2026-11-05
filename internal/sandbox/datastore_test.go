package sandbox

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/oklog/ulid/v2"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func populate(t *testing.T, ds storage.OpenFGADatastore) (storeID string) {
	t.Helper()
	ctx := context.Background()
	storeID = ulid.Make().String()
	_, err := ds.CreateStore(ctx, &openfgav1.Store{Id: storeID, Name: "s"})
	require.NoError(t, err)
	model := &openfgav1.AuthorizationModel{
		Id:            ulid.Make().String(),
		SchemaVersion: "1.1",
		TypeDefinitions: []*openfgav1.TypeDefinition{
			{Type: "user"},
			{Type: "doc", Relations: map[string]*openfgav1.Userset{
				"viewer": {Userset: &openfgav1.Userset_This{This: &openfgav1.DirectUserset{}}},
			}, Metadata: &openfgav1.Metadata{Relations: map[string]*openfgav1.RelationMetadata{
				"viewer": {DirectlyRelatedUserTypes: []*openfgav1.RelationReference{{Type: "user"}}},
			}}},
		},
	}
	require.NoError(t, ds.WriteAuthorizationModel(ctx, storeID, model))
	require.NoError(t, ds.Write(ctx, storeID, nil, []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "viewer", User: "user:anne"},
	}))
	return storeID
}

func TestOpenDatastore_CreatesAndMigrates(t *testing.T) {
	ds, err := OpenDatastore(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(ds.Close)
	storeID := populate(t, ds)
	tuples, _, err := ds.ReadPage(context.Background(), storeID, storage.ReadFilter{Object: "doc:1"}, storage.ReadPageOptions{Pagination: storage.NewPaginationOptions(10, "")})
	require.NoError(t, err)
	require.Len(t, tuples, 1)
}

func TestOpenDatastore_Reopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(path)
	require.NoError(t, err)
	storeID := populate(t, ds)
	ds.Close()

	ds, err = OpenDatastore(path)
	require.NoError(t, err)
	t.Cleanup(ds.Close)
	got, err := ds.GetStore(context.Background(), storeID)
	require.NoError(t, err)
	require.Equal(t, "s", got.GetName())
	m, err := ds.FindLatestAuthorizationModel(context.Background(), storeID)
	require.NoError(t, err)
	require.Len(t, m.GetTypeDefinitions(), 2)
}

func TestOpenDatastore_NewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(path)
	require.NoError(t, err)
	ds.Close()

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999999, 1)`)
	require.NoError(t, err)
	// Fold the WAL into the main file and close, so the hash is stable.
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	before := hashFiles(t, path)
	_, err = OpenDatastore(path)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrSchemaTooNew), "got %v", err)
	require.Equal(t, before, hashFiles(t, path), "file must be untouched")
}

func hashFiles(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	h := sha256.New()
	h.Write(b)
	for _, e := range entries { // also catches stray -wal/-shm files
		h.Write([]byte(e.Name()))
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
