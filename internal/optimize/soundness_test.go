package optimize

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// checkBoth runs one Check against the original model and against the
// rewritten model, each in its own store loaded with the same tuples.
func checkBoth(t *testing.T, eng modeltest.Engine, rw *RewriteResult, tuples []*openfgav1.TupleKey, req modeltest.CheckReq) (before, after bool) {
	t.Helper()
	ctx := context.Background()
	run := func(m *Model) bool {
		storeID, modelID, err := eng.Setup(ctx, m.Proto, tuples)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = eng.DeleteStore(ctx, storeID) }()
		got, err := eng.Check(ctx, modeltest.Scope{StoreID: storeID, ModelID: modelID}, req)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return run(rw.Before), run(rw.After)
}

// appliedAfter returns the After of the applied finding on typ#rel.
func appliedAfter(rw *RewriteResult, typ, rel string) (after string, ok bool) {
	for _, f := range rw.Applied {
		if f.Type == typ && f.Relation == rel {
			return f.After, true
		}
	}
	return "", false
}

func TestSoundness_KnownCounterexamples(t *testing.T) {
	const folderDoc = "model\n  schema 1.1\n\ntype user\n\ntype folder\n  relations\n    define editor: [user]\n    define viewer: editor\n\ntype doc\n  relations\n    define parent: [folder]\n    define can_view: editor from parent or viewer from parent\n"
	tuple := func(user, rel, obj string) []*openfgav1.TupleKey {
		return []*openfgav1.TupleKey{{User: user, Relation: rel, Object: obj}}
	}
	class2Tuples := func(t *testing.T) []*openfgav1.TupleKey {
		ctx, err := structpb.NewStruct(map[string]any{"x": 16})
		if err != nil {
			t.Fatal(err)
		}
		return []*openfgav1.TupleKey{
			{User: "user:anne", Relation: "viewer", Object: "folder:1", Condition: &openfgav1.RelationshipCondition{Name: "cx", Context: ctx}},
			{User: "user:*", Relation: "viewer", Object: "folder:1"},
			{User: "folder:1", Relation: "parent", Object: "folder:2"},
		}
	}
	tests := []struct {
		name      string
		model     func(t *testing.T) *Model
		tuples    []*openfgav1.TupleKey
		req       modeltest.CheckReq
		typ, rel  string
		wantAfter string
		wantOK    bool
	}{
		{
			name: "alias in union",
			model: func(t *testing.T) *Model {
				return loadInline(t, hdr+"    define a: [user]\n    define w: a\n    define v: a or w\n")
			},
			req: modeltest.CheckReq{User: "doc:1#w", Relation: "v", Object: "doc:1"},
			typ: "doc", rel: "v", wantAfter: "w", wantOK: true,
		},
		{
			name: "alias in intersection",
			model: func(t *testing.T) *Model {
				return loadInline(t, hdr+"    define b: [user]\n    define a: b\n    define r: a and b\n")
			},
			req: modeltest.CheckReq{User: "doc:1#a", Relation: "r", Object: "doc:1"},
			typ: "doc", rel: "r", wantAfter: "b", wantOK: true,
		},
		{
			name:   "alias via TTU",
			model:  func(t *testing.T) *Model { return loadInline(t, folderDoc) },
			tuples: tuple("folder:1", "parent", "doc:1"),
			req:    modeltest.CheckReq{User: "folder:1#viewer", Relation: "can_view", Object: "doc:1"},
			typ:    "doc", rel: "can_view", wantAfter: "viewer from parent", wantOK: true,
		},
		{
			name:   "reference model",
			model:  func(t *testing.T) *Model { return LoadReferenceModel(t) },
			tuples: tuple("folder:1", "parent", "document:1"),
			req:    modeltest.CheckReq{User: "folder:1#can_view", Relation: "can_view", Object: "document:1"},
			typ:    "document", rel: "can_view", wantOK: false,
		},
		{
			name:   "conditioned wildcard resolver class",
			model:  func(t *testing.T) *Model { return loadInline(t, class2DSL) },
			tuples: class2Tuples(t),
			req:    modeltest.CheckReq{User: "user:anne", Relation: "can_view", Object: "folder:2"},
			typ:    "folder", rel: "can_view", wantOK: false,
		},
	}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rw, err := Rewrite(tc.model(t))
			if err != nil {
				t.Fatal(err)
			}
			after, ok := appliedAfter(rw, tc.typ, tc.rel)
			if after != tc.wantAfter || ok != tc.wantOK {
				t.Errorf("appliedAfter(%s#%s) = (%q, %v), want (%q, %v); applied: %+v", tc.typ, tc.rel, after, ok, tc.wantAfter, tc.wantOK, rw.Applied)
			}
			b, a := checkBoth(t, eng, rw, tc.tuples, tc.req)
			if b != a {
				t.Errorf("answer changed: before=%v after=%v; applied: %+v", b, a, rw.Applied)
			}
		})
	}
}
