package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

func TestCountingEngine_CountsReadsAndDispatches(t *testing.T) {
	eng, counters, err := NewCountingEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	path := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(path, []byte(`model
  schema 1.1
type user
type org
  relations
    define member: [user]
type doc
  relations
    define parent: [doc]
    define org: [org]
    define viewer: [user] or member from org or viewer from parent
`), 0o600); err != nil {
		t.Fatal(err)
	}
	lm, err := loadModel(path)
	if err != nil {
		t.Fatal(err)
	}

	tuples := []*openfgav1.TupleKey{
		{User: "doc:2", Relation: "parent", Object: "doc:1"},
		{User: "doc:3", Relation: "parent", Object: "doc:2"},
		{User: "user:a", Relation: "viewer", Object: "doc:3"},
	}
	storeID, modelID, err := eng.Setup(context.Background(), lm.Proto, tuples)
	if err != nil {
		t.Fatal(err)
	}

	ctx, dispatches := WithDispatchTags(context.Background())
	counters.Reset()
	allowed, err := eng.Check(ctx, Scope{StoreID: storeID, ModelID: modelID}, CheckReq{User: "user:nobody", Relation: "viewer", Object: "doc:1"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Errorf("Check allowed = true, want false")
	}
	if got := counters.Reads(); got <= 2 {
		t.Errorf("Reads() = %d, want > 2", got)
	}
	if got := dispatches(); got <= 1 {
		t.Errorf("dispatches() = %v, want > 1", got)
	}

	counters.Reset()
	if got := counters.Reads(); got != 0 {
		t.Errorf("Reads() after Reset = %d, want 0", got)
	}
}
