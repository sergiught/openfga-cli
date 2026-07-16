package playground

import (
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/fga"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// After an add, the reloaded list should follow the just-written row instead of
// resetting to the top (TUI-34).
func TestPopulateTuplesFollowsPendingSelection(t *testing.T) {
	m := newPaneModel("", 100)
	m.tuples = []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:a", Relation: "viewer", Object: "doc:1"}},
		{Key: openfga.TupleKey{User: "user:b", Relation: "editor", Object: "doc:2"}},
		{Key: openfga.TupleKey{User: "user:c", Relation: "owner", Object: "doc:3"}},
	}
	m.tuplesList = uilist.New()
	m.tuplesList.SetSize(40, 12)

	want := fga.FormatTuple(m.tuples[2].Key)
	m.pendingTupleSelect = want
	m.populateTuples()

	sel, ok := m.tuplesList.Selected()
	if !ok || sel.ID != want {
		t.Fatalf("selection should follow the pending tuple %q, got ok=%v id=%q", want, ok, sel.ID)
	}
	if m.pendingTupleSelect != "" {
		t.Fatalf("pendingTupleSelect should be cleared after use, got %q", m.pendingTupleSelect)
	}
}
