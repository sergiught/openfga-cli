package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openIter(s *Session) component { return newIterHub(s, Focus{}) }

// identitiesDoc is the user.created sample with an iterator over its
// identities, holding one tuple.
func identitiesDoc() *mapping.Document {
	d := sampleDoc(userCreatedEvent())
	d.Rules[0].Iterator = &mapping.Iterator{
		Source: "input.data.object.identities",
		As:     "identity",
		Tuples: []mapping.Tuple{{Object: "connection:{{ identity.connection }}", Relation: "member", User: "user:1"}},
	}
	return d
}

func TestTheIteratorHubOffersItsTuplesAsARow(t *testing.T) {
	h := newHarness(t, identitiesDoc(), nil, openIter)
	c := topAs[*iterHub](t, h)
	if got, want := pickerTitles(c.hub), []string{"Source", "As", "Tuples"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	h.press("down", "down", "enter")
	l := topAs[*tupleList](t, h)
	if !l.at.InIter {
		t.Fatal("the Tuples row opened the rule's own tuples")
	}
	h.press("esc")
	topAs[*iterHub](t, h)
}

// Choosing Tuples before there is an iterator to hang them on says so rather
// than opening a list whose edits would be discarded.
func TestTheIteratorHubRefusesTuplesWithoutASource(t *testing.T) {
	h := newHarness(t, sampleDoc(userCreatedEvent()), nil, openIter)
	h.press("down", "down", "enter")
	topAs[*iterHub](t, h)
	if toast := h.lastToast(); !toast.Err || toast.Text != "set a source first" {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestTheHubRowsFollowTheDocument(t *testing.T) {
	h := newHarness(t, sampleDoc(userCreatedEvent()), nil, openIter)
	h.press("enter")
	h.typeText("input.data.object.identities")
	h.press("esc")
	view := plain(h.view(72, 12))
	if !strings.Contains(view, "input.data.object.identities") || !strings.Contains(view, "0 tuples") {
		t.Fatalf("the hub did not pick up the new source:\n%s", view)
	}
}

func TestTypingIntoIteratorSourceStaysOnTheForm(t *testing.T) {
	h := newHarness(t, sampleDoc(userCreatedEvent()), nil, openIter)
	h.press("enter")
	h.typeText("input.data.object.accounts")
	f := topAs[*iterForm](t, h)
	if got := f.form.Values()[0]; got != "input.data.object.accounts" {
		t.Fatalf("source = %q", got)
	}
	if it := h.s.Doc.Rules[0].Iterator; it == nil || it.Source != "input.data.object.accounts" {
		t.Fatalf("iterator = %+v, want the source committed as typed", it)
	}
}

// The source is blank for a moment whenever it is retyped. Dropping the
// iterator then would take its tuples with it, so only leaving the form drops it.
func TestABlankSourceMidRetypeKeepsTheIterator(t *testing.T) {
	h := newHarness(t, identitiesDoc(), nil, openIter)
	h.press("enter")
	backspace(h, len("input.data.object.identities"))
	h.send(tea.PasteMsg{Content: ""})
	if it := h.s.Doc.Rules[0].Iterator; it == nil || len(it.Tuples) != 1 {
		t.Fatalf("iterator = %+v, want it kept while the source is being retyped", it)
	}
}

func TestClearingTheIteratorSourceRemovesIt(t *testing.T) {
	h := newHarness(t, identitiesDoc(), nil, openIter)
	h.press("enter")
	backspace(h, len("input.data.object.identities"))
	h.press("esc")
	if it := h.s.Doc.Rules[0].Iterator; it != nil {
		t.Fatalf("iterator = %+v, want a blank source to drop it on the way out", it)
	}
	topAs[*iterHub](t, h)
}

func TestAsFocusesTheSecondField(t *testing.T) {
	h := newHarness(t, identitiesDoc(), nil, openIter)
	h.press("down", "enter")
	if got := topAs[*iterForm](t, h).form.FocusedIndex(); got != 1 {
		t.Fatalf("focus = %d, want As", got)
	}
}

// A path picked for the source goes in bare: the source is an expression.
// (The filter names one path only, so the test does not lean on match order.)
func TestAPathGoesIntoTheSourceBare(t *testing.T) {
	h := newHarness(t, sampleDoc(memberAddedEvent()), nil, openIter)
	h.press("enter", "ctrl+p", "/")
	h.typeText("organization.id")
	h.press("enter", "enter")
	if it := h.s.Doc.Rules[0].Iterator; it == nil || it.Source != "input.data.object.organization.id" {
		t.Fatalf("iterator = %+v", it)
	}
}

// A13 end to end: an iterator whose source only mapper can evaluate still
// offers its item's paths to the tuples inside it.
func TestTheTuplesOfAJSONPathIteratorCanPickTheItemsPaths(t *testing.T) {
	h := newHarness(t, sampleDoc(userCreatedEvent()), nil, openIter)
	h.press("enter")
	h.typeText(`json_path(input, "data.object.identities")`)
	h.press("tab")
	h.typeText("identity")
	h.press("esc", "down", "down", "enter", "a")
	topAs[*tupleForm](t, h)
	h.press("ctrl+p")
	if ids := pathIDs(topAs[*pathPicker](t, h)); !slices.Contains(ids, "identity.connection") {
		t.Fatalf("paths = %v, want identity.connection", ids)
	}
}

func TestTheIteratorHelpNamesTheLimitsNoSampleCanShow(t *testing.T) {
	_, body := newIterHub(NewSession(t.Context(), sampleDoc(userCreatedEvent()), nil), Focus{}).help()
	for _, want := range []string{"1000", "40"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the iterator help does not name %s:\n%s", want, body)
		}
	}
}

func TestTabbingThroughAnIteratorChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, identitiesDoc,
		func(s *Session) component { return newIterForm(s, Focus{}, 0) })
}

// A multi-line source, from a block scalar that ends in a newline, survives
// opening the iterator and leaving it.
func TestTabbingThroughAMultilineIteratorSourceChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		d := identitiesDoc()
		d.Rules[0].Iterator.Source = "input.data.object.identities\n??\t[]\n"
		return d
	}, func(s *Session) component { return newIterForm(s, Focus{}, 0) })
}
