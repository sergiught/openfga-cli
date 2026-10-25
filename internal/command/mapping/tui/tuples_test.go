package tui

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openTuples(s *Session) component { return newTupleList(s, Focus{}) }

func abcTuples() *mapping.Document {
	return tuplesDoc(
		mapping.Tuple{User: "user:alpha", Relation: "member", Object: "organization:a"},
		mapping.Tuple{User: "user:bravo", Relation: "member", Object: "organization:b"},
		mapping.Tuple{User: "user:charlie", Relation: "member", Object: "organization:c"},
	)
}

func users(d *mapping.Document) string {
	var out []string
	for _, t := range d.Rules[0].Tuples {
		out = append(out, t.User)
	}
	return strings.Join(out, ",")
}

func TestTuplesListEmptyStateAndAdd(t *testing.T) {
	h := newHarness(t, tuplesDoc(), nil, openTuples)
	if !strings.Contains(plain(h.view(60, 12)), "No tuples yet.") {
		t.Fatalf("the empty state is missing:\n%s", plain(h.view(60, 12)))
	}
	h.press("a")
	topAs[*tupleForm](t, h)
	if n := len(h.s.Doc.Rules[0].Tuples); n != 1 || h.changes == 0 {
		t.Fatalf("tuples = %d, changes = %d", n, h.changes)
	}
}

func TestDeleteTuple(t *testing.T) {
	h := newHarness(t, abcTuples(), nil, openTuples)
	h.press("d")
	if got := users(h.s.Doc); got != "user:bravo,user:charlie" {
		t.Fatalf("tuples = %s", got)
	}
}

// A2: the filter's matches were never recomputed, so the selection stayed on
// the first row and d deleted a tuple the user could not see.
func TestAFilteredDeleteRemovesTheRowShown(t *testing.T) {
	h := newHarness(t, abcTuples(), nil, openTuples)
	h.press("/")
	h.typeText("charlie")
	h.press("enter", "d")
	if got := users(h.s.Doc); got != "user:alpha,user:bravo" {
		t.Fatalf("tuples = %s, want charlie deleted", got)
	}
}

// Review focus: after an edit the list re-applies its filter to the new rows.
// The edited row no longer matches, so nothing is selected and d deletes
// nothing — not whatever row happens to be first.
func TestDeleteAfterTheFilteredRowStopsMatchingDeletesNothing(t *testing.T) {
	h := newHarness(t, abcTuples(), nil, openTuples)
	h.press("/")
	h.typeText("charlie")
	h.press("enter", "enter")
	f := topAs[*tupleForm](t, h)
	f.form.SetValues(tupleValues(map[tupleField]string{
		fieldObject: "organization:c", fieldRelation: "member", fieldUser: "user:delta",
	}))
	h.press("esc", "d")
	if got := users(h.s.Doc); got != "user:alpha,user:bravo,user:delta" {
		t.Fatalf("tuples = %s, want all three kept", got)
	}
}

// ? is a character while the filter takes input, not a request for help.
func TestQuestionMarkIsTextInTheTupleFilter(t *testing.T) {
	h := newHarness(t, abcTuples(), nil, openTuples)
	h.press("/", "?")
	c := topAs[*tupleList](t, h)
	if !c.filtering() || !strings.Contains(c.list.Model.FilterValue(), "?") {
		t.Fatalf("filtering = %v, filter = %q", c.filtering(), c.list.Model.FilterValue())
	}
}

func TestEscClosesTheList(t *testing.T) {
	h := newHarness(t, abcTuples(), nil, openTuples)
	h.press("esc")
	if !h.closed {
		t.Fatal("esc did not close the list")
	}
}

// Two different lists render here — the rule's own tuples and the iterator's —
// and the heading is all that tells them apart.
func TestTheIteratorsTupleListSaysSo(t *testing.T) {
	d := tuplesDoc()
	d.Rules[0].Iterator = &mapping.Iterator{Source: "input.data.object.identities", As: "identity"}
	c := newTupleList(NewSession(t.Context(), d, nil), Focus{InIter: true})
	title, sub := c.heading()
	if title != "Iterator tuples" || !strings.Contains(sub, "input.data.object.identities") {
		t.Fatalf("heading = %q, %q", title, sub)
	}
}

// The list re-reads the document when the form over it closes, so the row
// shows the tuple as the form left it.
func TestTheListShowsTheTupleTheFormWrote(t *testing.T) {
	h := newHarness(t, tuplesDoc(mapping.Tuple{}), nil, openTuples)
	h.press("enter")
	f := topAs[*tupleForm](t, h)
	f.form.SetValues(tupleValues(map[tupleField]string{
		fieldObject: "organization:x", fieldRelation: "member", fieldUser: "user:1",
	}))
	h.press("esc")
	topAs[*tupleList](t, h)
	if v := plain(h.view(60, 12)); !strings.Contains(v, "user:1 member organization:x") {
		t.Fatalf("the list does not show the edited tuple:\n%s", v)
	}
}

// The iterator's tuple list adds to the iterator's tuples and the rule's own
// list to the rule's, whichever was open before.
func TestEachTupleListAddsToItsOwnList(t *testing.T) {
	s := NewSession(t.Context(), identitiesDoc(), nil)
	for _, at := range []Focus{{InIter: true}, {}, {InIter: true}} {
		r := s.Doc.Rules[0]
		own, iter := len(r.Tuples), len(r.Iterator.Tuples)
		h := &harness{t: t, s: s, st: newStack(s, newTupleList(s, at))}
		h.st.Init()
		h.press("a", "tab", "tab")
		h.typeText("organization:acme")
		h.press("esc")
		r = s.Doc.Rules[0]
		wantOwn, wantIter := own, iter
		if at.InIter {
			wantIter++
		} else {
			wantOwn++
		}
		if len(r.Tuples) != wantOwn || len(r.Iterator.Tuples) != wantIter {
			t.Fatalf("at %+v: the rule has %d tuples and its iterator %d, want %d and %d",
				at, len(r.Tuples), len(r.Iterator.Tuples), wantOwn, wantIter)
		}
		list := r.Tuples
		if at.InIter {
			list = r.Iterator.Tuples
		}
		if got := list[len(list)-1].Object; got != "organization:acme" {
			t.Fatalf("at %+v: the new tuple's object is %q", at, got)
		}
	}
}
