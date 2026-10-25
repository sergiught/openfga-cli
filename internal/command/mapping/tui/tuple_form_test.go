package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openForm(s *Session) component { return newTupleForm(s, Focus{}, 0) }

func formOn(t *testing.T, d *mapping.Document, ix *mapping.ModelIndex) (*harness, *tupleForm) {
	t.Helper()
	h := newHarness(t, d, ix, openForm)
	return h, topAs[*tupleForm](t, h)
}

func TestTypingCommitsAsYouType(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.FocusIndex(int(fieldObject))
	h.typeText("organization:acme")
	if got := h.s.Doc.Rules[0].Tuples[0].Object; got != "organization:acme" || h.changes == 0 {
		t.Fatalf("object = %q, changes = %d", got, h.changes)
	}
}

func TestCommitWritesTheTupleBack(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.SetValues(tupleValues(map[tupleField]string{
		fieldObject:   "organization:{{ input.data.object.organization.id }}",
		fieldRelation: "member",
		fieldUser:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		fieldWhen:     `input.data.object.user.user_id != ""`,
	}))
	h.press("esc")
	got := h.s.Doc.Rules[0].Tuples[0]
	want := mapping.Tuple{
		Object:   "organization:{{ input.data.object.organization.id }}",
		Relation: "member",
		User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		When:     `input.data.object.user.user_id != ""`,
	}
	if !reflect.DeepEqual(got, want) || !h.closed {
		t.Fatalf("tuple = %+v, closed = %v", got, h.closed)
	}
}

// A12: from Condition, with Context hidden, enter wrapped to Object forever.
func TestEnterOnTheLastVisibleFieldLeavesTheForm(t *testing.T) {
	h, _ := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	h.typeText("user:1")
	h.press("tab")
	h.typeText("member")
	h.press("tab")
	h.typeText("organization:acme")
	for i := 0; i < 6 && !h.closed; i++ {
		h.press("enter")
	}
	if !h.closed {
		t.Fatal("enter never submitted the tuple form")
	}
}

func TestObjectPickerPrefillsTypeAndTemplate(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(testModel()))
	f.form.FocusIndex(int(fieldObject))
	h.press("ctrl+o")
	if f.pick == nil || !selectTitle(f.pick, "organization") {
		t.Fatal("the model picker did not offer organization")
	}
	h.press("enter")
	if f.pick != nil {
		t.Fatal("the picker should close after a choice")
	}
	want := "organization:{{  }}"
	if got := f.form.Values()[fieldObject]; got != want {
		t.Fatalf("object = %q, want %q", got, want)
	}
	if got := f.form.Cursor(int(fieldObject)); got != len("organization:{{ ") {
		t.Fatalf("caret = %d, want it between the braces", got)
	}
	// A3: the pick is committed like a keystroke.
	if got := h.s.Doc.Rules[0].Tuples[0].Object; got != want {
		t.Fatalf("document object = %q, want %q", got, want)
	}
}

func TestRelationPickerOffersOnlyThatTypesRelations(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Object: "organization:x"}), mapping.IndexModel(testModel()))
	f.form.FocusIndex(int(fieldRelation))
	h.press("ctrl+o")
	got := pickerTitles(f.pick)
	slices.Sort(got)
	if want := []string{"Other…", "admin", "member"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relations = %v, want %v", got, want)
	}
	selectTitle(f.pick, "member")
	h.press("enter")
	if got := f.form.Values()[fieldRelation]; got != "member" {
		t.Fatalf("relation = %q", got)
	}
}

func TestUserPickerUsesDirectlyRelatedTypes(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Object: "organization:x", Relation: "member"}), mapping.IndexModel(testModel()))
	f.form.FocusIndex(int(fieldUser))
	h.press("ctrl+o")
	if !selectTitle(f.pick, "user") {
		t.Fatalf("user types = %v", pickerTitles(f.pick))
	}
	h.press("enter")
	if got := f.form.Values()[fieldUser]; got != "user:{{ fga_escape() }}" {
		t.Fatalf("user = %q", got)
	}
}

func TestUsersetAndWildcardPrefills(t *testing.T) {
	for in, want := range map[string]string{
		"team#member": "team:{{  }}#member",
		"user:*":      "user:*",
		"user":        "user:{{ fga_escape() }}",
	} {
		if got := userPrefill(in); got != want {
			t.Fatalf("userPrefill(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOtherLeavesTheFieldAlone(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Object: "organization:x"}), mapping.IndexModel(testModel()))
	f.form.FocusIndex(int(fieldObject))
	h.press("ctrl+o")
	selectTitle(f.pick, otherLabel)
	h.press("enter")
	if got := f.form.Values()[fieldObject]; got != "organization:x" || f.pick != nil {
		t.Fatalf("object = %q, picker open = %v", got, f.pick != nil)
	}
}

func TestWithoutAModelEveryFieldIsFreeText(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	h.press("ctrl+o")
	if f.pick != nil {
		t.Fatal("a picker opened with no model")
	}
	if toast := h.lastToast(); toast.Err || !strings.Contains(toast.Text, "No model loaded") {
		t.Fatalf("toast = %+v", toast)
	}
	f.form.FocusIndex(int(fieldObject))
	h.typeText("organization:1")
	if got := f.form.Values()[fieldObject]; got != "organization:1" {
		t.Fatalf("object = %q", got)
	}
}

func TestPasteWhileTheFieldPickerIsOpenDoesNotEditTheHiddenForm(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(testModel()))
	h.press("ctrl+o")
	h.send(tea.PasteMsg{Content: "pasted"})
	if got := f.form.Values()[fieldObject]; got != "" {
		t.Fatalf("object = %q, want the hidden form untouched", got)
	}
}

func TestActionFieldIsHiddenWhenTheRuleSetsOne(t *testing.T) {
	if _, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil); !f.form.Visible(int(fieldAction)) {
		t.Fatal("the action should be offered when the rule has none")
	}
	d := tuplesDoc(mapping.Tuple{})
	d.Rules[0].Action = "delete"
	_, f := formOn(t, d, nil)
	if f.form.Visible(int(fieldAction)) {
		t.Fatal("the per-tuple action is offered under a rule-level one")
	}
}

func TestConditionIsHiddenForDeletes(t *testing.T) {
	_, f := formOn(t, tuplesDoc(mapping.Tuple{Action: "delete"}), nil)
	if f.form.Visible(int(fieldCondition)) || f.form.Visible(int(fieldContext)) {
		t.Fatal("a delete offers a condition")
	}
	if _, f := formOn(t, tuplesDoc(mapping.Tuple{Action: "write"}), nil); !f.form.Visible(int(fieldCondition)) {
		t.Fatal("a write may carry a condition")
	}
}

func TestCommitDropsFieldsTheFormIsHiding(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.SetValues(tupleValues(map[tupleField]string{
		fieldObject: "organization:x", fieldRelation: "member", fieldUser: "user:1",
		fieldAction: "delete", fieldCondition: "in_region",
		fieldContext: "region={{ input.data.object.region }}",
	}))
	h.press("tab")
	if got := h.s.Doc.Rules[0].Tuples[0]; got.Condition != "" || len(got.Context) != 0 || got.Action != "delete" {
		t.Fatalf("tuple = %+v, want the hidden condition and context dropped", got)
	}
}

func TestConditionContextKeysComeFromTheModel(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(conditionModel()))
	f.form.SetValues(tupleValues(map[tupleField]string{fieldCondition: "in_region"}))
	h.press("tab")
	if !reflect.DeepEqual(f.ctxKeys, []string{"region"}) {
		t.Fatalf("context keys = %v", f.ctxKeys)
	}
	if !strings.Contains(plain(h.view(72, 30)), "condition parameters: region") {
		t.Fatalf("the parameters line is missing:\n%s", plain(h.view(72, 30)))
	}
}

func TestTheModelPickerIsOnlyOfferedWhenThereIsAModel(t *testing.T) {
	_, without := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	_, with := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(testModel()))
	if slices.Contains(without.Keys(), "ctrl+o pick from model") || !slices.Contains(with.Keys(), "ctrl+o pick from model") {
		t.Fatalf("without a model %v; with one %v", without.Keys(), with.Keys())
	}
}

// SetValues starts the caret at 0, so a long prefilled value shows its start:
// the type the reader needs, not a tail that reads as a corrupted value.
func TestAPreFilledFieldOpensAtTheStartOfItsValue(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Object: "organization:{{ input.data.object.organization.id }}"}), nil)
	if got := f.form.Cursor(int(fieldObject)); got != 0 {
		t.Fatalf("caret = %d, want 0", got)
	}
	if v := plain(h.view(40, 30)); !strings.Contains(v, "organization:{{ input") {
		t.Fatalf("the field does not show the start of its value:\n%s", v)
	}
}

func TestTabbingOffABadValueFlagsItInline(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.FocusIndex(int(fieldObject))
	h.typeText("organization")
	h.press("tab")
	if !strings.Contains(plain(h.view(72, 30)), "must be type:id") {
		t.Fatalf("no inline error after tabbing off a bad object:\n%s", plain(h.view(72, 30)))
	}
}

// A3: the inserted path reaches the document, not only the form.
func TestAPathInsertedIntoTheFormReachesTheDocument(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.FocusIndex(int(fieldUser))
	h.typeText("user:")
	h.press("ctrl+p")
	topAs[*pathPicker](t, h)
	h.press("/")
	h.typeText("user_id")
	h.press("enter", "enter")
	want := "user:{{ input.data.object.user.user_id }}"
	if got := topAs[*tupleForm](t, h).form.Values()[fieldUser]; got != want {
		t.Fatalf("form user = %q", got)
	}
	if got := h.s.Doc.Rules[0].Tuples[0].User; got != want {
		t.Fatalf("document user = %q, want %q", got, want)
	}
}

func TestEscClosesThePathPickWithoutChanging(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.FocusIndex(int(fieldObject))
	h.typeText("organization:")
	h.press("ctrl+p", "esc")
	if got := topAs[*tupleForm](t, h).form.Values()[fieldObject]; got != "organization:" || f != h.top() {
		t.Fatalf("object = %q", got)
	}
}

func TestCtrlPWithoutASampleSaysWhy(t *testing.T) {
	d := tuplesDoc(mapping.Tuple{})
	d.Rules[0].Sample = nil
	h, _ := formOn(t, d, nil)
	h.press("ctrl+p")
	topAs[*tupleForm](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.Contains(toast.Text, "pick a sample event first") {
		t.Fatalf("toast = %+v", toast)
	}
}

// A6: the validator ignored commas inside {{ }} and the commit did not, so the
// form accepted a value the document then cut in half.
func TestContextSplitsOnlyOutsideTemplates(t *testing.T) {
	got := splitContext(`ids={{ join(x, ",") }}, region={{ input.r }}`)
	want := []mapping.ContextEntry{{Key: "ids", Template: `{{ join(x, ",") }}`}, {Key: "region", Template: "{{ input.r }}"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitContext = %+v, want %+v", got, want)
	}
	if err := vContextPairs(`ids={{ join(x, ",") }}`); err != nil {
		t.Fatalf("the validator rejects a comma inside a template: %v", err)
	}
	if err := vContextPairs(`a=1, b`); err == nil {
		t.Fatal("the validator accepts a pair with no =")
	}
	if err := vContextPairs(`{{ a == b }}`); err == nil {
		t.Fatal("the validator takes an = inside a template for the pair's own")
	}
}

func TestAContextWithACommaInATemplateIsCommittedWhole(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Condition: "in_region"}), nil)
	f.form.FocusIndex(int(fieldContext))
	h.typeText(`ids={{ join(x, ",") }}`)
	h.press("esc")
	ctx := h.s.Doc.Rules[0].Tuples[0].Context
	if len(ctx) != 1 || ctx[0].Template != `{{ join(x, ",") }}` {
		t.Fatalf("context = %+v", ctx)
	}
}

// Writing delete hides the condition and its context; an invalid context left
// behind there sent enter to a field nobody could see, and the form never left.
func TestEnterLeavesTheFormPastAHiddenInvalidContext(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.SetValues(tupleValues(map[tupleField]string{
		fieldObject: "organization:x", fieldRelation: "member", fieldUser: "user:1",
		fieldCondition: "in_region", fieldContext: "region",
	}))
	f.form.FocusIndex(int(fieldAction))
	h.press("right") // write → delete
	if f.form.Visible(int(fieldContext)) {
		t.Fatal("a delete still offers the context")
	}
	h.press("enter")
	if !h.closed {
		t.Fatalf("enter did not leave the form: focus = %d", f.form.FocusedIndex())
	}
	if got := h.s.Doc.Rules[0].Tuples[0]; got.Action != "delete" || got.Condition != "" || len(got.Context) != 0 {
		t.Fatalf("tuple = %+v, want a delete with the hidden fields dropped", got)
	}
}

// Moving through a tuple and leaving it is not an edit: the unsaved flag and
// the quit prompt read Outcome.Changed.
func TestTabbingThroughATupleChangesNothing(t *testing.T) {
	assertOnlyEditsChangeTheDocument(t, func() *mapping.Document {
		return tuplesDoc(mapping.Tuple{
			User: "user:1", Relation: "member", Object: "organization:1", Condition: "in_region",
			Context: []mapping.ContextEntry{{Key: "region", Template: "{{ input.region }}"}},
		})
	}, openForm)
}

// The action is a choice of two, so it is picked with ←→ like the rule's own
// action, and a typo like "delet" cannot reach the file. Leaving it on write
// keeps an unset action unset rather than writing the default out.
func TestTheTupleActionIsPicked(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	f.form.FocusIndex(int(fieldAction))
	if got := f.form.Values()[fieldAction]; got != "write" {
		t.Fatalf("an unset action shows %q, want write", got)
	}
	h.press("right")
	if got := h.s.Doc.Rules[0].Tuples[0].Action; got != "delete" {
		t.Fatalf("action = %q after ←→, want delete", got)
	}
	h.press("left")
	if got := h.s.Doc.Rules[0].Tuples[0].Action; got != "" {
		t.Fatalf("back on write, action = %q, want it unset again", got)
	}
}

// An action the file spells out stays spelled out.
func TestAnExplicitWriteSurvivesTheForm(t *testing.T) {
	h, _ := formOn(t, tuplesDoc(mapping.Tuple{Object: "organization:1", Relation: "member", User: "user:1", Action: "write"}), nil)
	h.press("tab", "tab", "tab", "tab", "tab")
	if got := h.s.Doc.Rules[0].Tuples[0].Action; got != "write" || h.changes != 0 {
		t.Fatalf("action = %q, changes = %d; want write kept and nothing changed", got, h.changes)
	}
}

// OpenFGA writes a tuple as user, relation, object; the form asks in that order.
func TestTheTupleFormAsksUserFirst(t *testing.T) {
	_, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	got := plain(f.form.View())
	last := -1
	for _, label := range []string{"User", "Relation", "Object", "When (optional)", "Action", "Condition (optional)"} {
		i := strings.Index(got, label)
		if i < 0 || i <= last {
			t.Fatalf("%q is missing or out of order in:\n%s", label, got)
		}
		last = i
	}
	if fieldUser != 0 || fieldRelation != 1 || fieldObject != 2 {
		t.Fatalf("field indexes = %d %d %d, want 0 1 2", fieldUser, fieldRelation, fieldObject)
	}
}

func TestTabWalksUserRelationObject(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	for _, want := range []tupleField{fieldUser, fieldRelation, fieldObject} {
		if got := f.form.FocusedIndex(); got != int(want) {
			t.Fatalf("focus = %d, want %d", got, want)
		}
		h.typeText("x")
		h.press("tab")
	}
	if got := f.form.FocusedIndex(); got != int(fieldWhen) {
		t.Fatalf("focus after the object = %d, want When", got)
	}
	tp := h.s.Doc.Rules[0].Tuples[0]
	if tp.User != "x" || tp.Relation != "x" || tp.Object != "x" || tp.When != "" {
		t.Fatalf("tuple = %+v: typing did not land in user, relation, object", tp)
	}
}

func TestARelationPickOnAFreshTupleOffersEveryRelation(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(testModel()))
	f.form.FocusIndex(int(fieldRelation))
	h.press("ctrl+o")
	if f.pick == nil {
		t.Fatal("no relation picker on a tuple with no object yet")
	}
	got := pickerTitles(f.pick)
	slices.Sort(got)
	if want := []string{"Other…", "admin", "member"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relations = %v, want %v", got, want)
	}
}

// twoSubjectModel: organization#member takes a user, team#lead a team userset.
func twoSubjectModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		ID: "01J0",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "organization", Relations: map[string]openfga.Userset{"member": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
				}}},
			{Type: "project", Relations: map[string]openfga.Userset{"lead": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"lead": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "team", Relation: "member"}}},
				}}},
		},
	}
}

func TestAUserPickOnAFreshTupleOffersEveryUserType(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{}), mapping.IndexModel(twoSubjectModel()))
	f.form.FocusIndex(int(fieldUser))
	h.press("ctrl+o")
	if f.pick == nil {
		t.Fatal("no user picker on a tuple with no object yet")
	}
	got := pickerTitles(f.pick)
	slices.Sort(got)
	if want := []string{"Other…", "team#member", "user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("user types = %v, want %v", got, want)
	}
}

func TestAUserPickNarrowsToTheChosenRelation(t *testing.T) {
	h, f := formOn(t, tuplesDoc(mapping.Tuple{Relation: "lead"}), mapping.IndexModel(twoSubjectModel()))
	f.form.FocusIndex(int(fieldUser))
	h.press("ctrl+o")
	if f.pick == nil {
		t.Fatal("no user picker")
	}
	got := pickerTitles(f.pick)
	slices.Sort(got)
	if want := []string{"Other…", "team#member"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("user types for lead = %v, want %v", got, want)
	}
}
