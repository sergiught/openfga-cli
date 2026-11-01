package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/stretchr/testify/require"
)

func TestCountingEngine_CountsReadsAndDispatches(t *testing.T) {
	eng, counters, err := NewCountingEmbeddedEngine(nil)
	require.NoError(t, err)
	defer func() { _ = eng.Close() }()

	path := filepath.Join(t.TempDir(), "model.fga")
	require.NoError(t, os.WriteFile(path, []byte(`model
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
`), 0o600))
	lm, err := loadModel(path)
	require.NoError(t, err)

	tuples := []*openfgav1.TupleKey{
		{User: "doc:2", Relation: "parent", Object: "doc:1"},
		{User: "doc:3", Relation: "parent", Object: "doc:2"},
		{User: "user:a", Relation: "viewer", Object: "doc:3"},
	}
	storeID, modelID, err := eng.Setup(context.Background(), lm.Proto, tuples)
	require.NoError(t, err)

	ctx, dispatches := WithDispatchTags(context.Background())
	counters.Reset()
	allowed, err := eng.Check(ctx, Scope{StoreID: storeID, ModelID: modelID}, CheckReq{User: "user:nobody", Relation: "viewer", Object: "doc:1"})
	require.NoError(t, err)
	require.False(t, allowed)
	require.Greater(t, counters.Reads(), int64(2))
	require.Greater(t, dispatches(), float64(1))

	counters.Reset()
	require.Equal(t, int64(0), counters.Reads())
}
