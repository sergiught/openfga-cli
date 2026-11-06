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
	_ "modernc.org/sqlite"
)

func populate(t *testing.T, ds storage.OpenFGADatastore) (storeID string) {
	t.Helper()
	ctx := context.Background()
	storeID = ulid.Make().String()
	_, err := ds.CreateStore(ctx, &openfgav1.Store{Id: storeID, Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := ds.WriteAuthorizationModel(ctx, storeID, model); err != nil {
		t.Fatal(err)
	}
	if ds.Write(ctx, storeID, nil, []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "viewer", User: "user:anne"},
	}) != nil {
		t.Fatal(ds.Write(ctx, storeID, nil, []*openfgav1.TupleKey{
			{Object: "doc:1", Relation: "viewer", User: "user:anne"},
		}))
	}
	return storeID
}

func TestOpenDatastore_CreatesAndMigrates(t *testing.T) {
	ds, err := OpenDatastore(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ds.Close)
	storeID := populate(t, ds)
	tuples, _, err := ds.ReadPage(context.Background(), storeID, storage.ReadFilter{Object: "doc:1"}, storage.ReadPageOptions{Pagination: storage.NewPaginationOptions(10, "")})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(tuples); got != 1 {
		t.Fatalf("len = %d, want %d", got, 1)
	}
}

func TestOpenDatastore_Reopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(path)
	if err != nil {
		t.Fatal(err)
	}
	storeID := populate(t, ds)
	ds.Close()

	ds, err = OpenDatastore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ds.Close)
	got, err := ds.GetStore(context.Background(), storeID)
	if err != nil {
		t.Fatal(err)
	}
	if got := got.GetName(); got != "s" {
		t.Fatalf("got %v, want %v", got, "s")
	}
	m, err := ds.FindLatestAuthorizationModel(context.Background(), storeID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.GetTypeDefinitions()); got != 2 {
		t.Fatalf("len = %d, want %d", got, 2)
	}
}

func TestOpenDatastore_NewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	ds, err := OpenDatastore(path)
	if err != nil {
		t.Fatal(err)
	}
	ds.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999999, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	// Fold the WAL into the main file and close, so the hash is stable.
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before := hashFiles(t, path)
	_, err = OpenDatastore(path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !(errors.Is(err, ErrSchemaTooNew)) {
		t.Fatalf("got %v, want ErrSchemaTooNew", err)
	}
	if got := hashFiles(t, path); got != before {
		t.Fatalf("file must be untouched: hash %v, want %v", got, before)
	}
}

func hashFiles(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write(b)
	for _, e := range entries { // also catches stray -wal/-shm files
		h.Write([]byte(e.Name()))
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
