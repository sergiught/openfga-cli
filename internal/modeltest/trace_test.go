package modeltest

import (
	"path/filepath"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// treeHasLabel reports whether any node in the ExplainNode tree has a label
// containing sub.
func treeHasLabel(n *ExplainNode, sub string) bool {
	if n == nil {
		return false
	}
	if strings.Contains(n.Label, sub) {
		return true
	}
	for _, c := range n.Children {
		if treeHasLabel(c, sub) {
			return true
		}
	}
	return false
}

func TestTraceExplainsOwnerGrant(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	lm, err := loadModel(filepath.Join("testdata", "docs", "model.fga"))
	if err != nil {
		t.Fatal(err)
	}

	tuples := []*openfgav1.TupleKey{{User: "user:anne", Relation: "owner", Object: "document:1"}}
	sc, err := setupScope(t, eng, lm.Proto, tuples)
	if err != nil {
		t.Fatal(err)
	}

	exp, err := trace(t.Context(), lm, eng, sc, CheckReq{User: "user:anne", Relation: "viewer", Object: "document:1"})
	if err != nil {
		t.Fatal(err)
	}

	if !exp.Verdict {
		t.Fatal("owner should resolve viewer verdict=true")
	}
	if !treeHasLabel(exp.Tree, "owner") {
		t.Fatalf("explanation tree should contain an 'owner' node; got %+v", exp.Tree)
	}
}
