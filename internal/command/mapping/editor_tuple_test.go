package mapping

import (
	"context"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// atTuples returns a wizard with a model, one rule, sitting on the tuples list.
func atTuples(t *testing.T) *wizardModel {
	t.Helper()
	m := atModelSource(t, func(ctx context.Context) (*openfga.AuthorizationModel, error) {
		return testModel(), nil
	})
	selectSource(t, m, "server")
	send(m, key("enter"))
	m.Update(m.loadCmd())
	addRuleAtTrigger(m) // add a rule; lands on trigger
	send(m, key("esc")) // -> rule hub
	m.sections.SetCursor(5)
	send(m, key("enter"))
	if m.top() != screenTuples {
		t.Fatalf("top = %v", m.top())
	}
	return m
}

func TestTuplesListEmptyStateAndAdd(t *testing.T) {
	m := atTuples(t)
	if !strings.Contains(strings.ToLower(m.viewString()), "add") {
		t.Fatalf("empty state should explain `a`:\n%s", m.viewString())
	}
	send(m, key("a"))
	if m.top() != screenTuple {
		t.Fatalf("top = %v", m.top())
	}
	if got := len(*m.tuples()); got != 1 {
		t.Fatalf("tuples = %d", got)
	}
}

func TestObjectPickerPrefillsTypeAndTemplate(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))

	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+o")) // open the picker for the focused field
	if m.fieldPick == nil {
		t.Fatal("expected a picker with a model loaded")
	}
	// The model has user and organization; pick organization.
	if !selectByTitle(m.fieldPick, "organization") {
		t.Fatalf("organization not offered: %v", titles(m.fieldPick))
	}
	send(m, key("enter"))

	if got := m.tupleForm.Values()[fieldObject]; got != "organization:{{  }}" {
		t.Fatalf("object = %q", got)
	}
	if m.fieldPick != nil {
		t.Fatal("the picker should close after a choice")
	}
}

func TestRelationPickerOffersOnlyThatTypesRelations(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldObject: "organization:{{ x }}"}))

	m.tupleForm.FocusIndex(int(fieldRelation))
	send(m, key("ctrl+o"))
	got := titles(m.fieldPick)
	if !contains(got, "admin") || !contains(got, "member") {
		t.Fatalf("relations = %v", got)
	}
	if contains(got, "user") {
		t.Fatalf("types leaked into the relation picker: %v", got)
	}

	selectByTitle(m.fieldPick, "member")
	send(m, key("enter"))
	if got := m.tupleForm.Values()[fieldRelation]; got != "member" {
		t.Fatalf("relation = %q", got)
	}
}

func TestUserPickerUsesDirectlyRelatedTypes(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{
		fieldObject:   "organization:{{ x }}",
		fieldRelation: "member",
	}))

	m.tupleForm.FocusIndex(int(fieldUser))
	send(m, key("ctrl+o"))
	if got := titles(m.fieldPick); !contains(got, "user") {
		t.Fatalf("user types = %v", got)
	}
	selectByTitle(m.fieldPick, "user")
	send(m, key("enter"))
	if got := m.tupleForm.Values()[fieldUser]; got != "user:{{ fga_escape() }}" {
		t.Fatalf("user = %q", got)
	}
}

func TestUsersetAndWildcardPrefills(t *testing.T) {
	if got := userPrefill("group#member"); got != "group:{{  }}#member" {
		t.Fatalf("userset = %q", got)
	}
	if got := userPrefill("user:*"); got != "user:*" {
		t.Fatalf("wildcard = %q", got)
	}
	// The plain case carries fga_escape, matching the field's placeholder:
	// subject ids come from the provider and need escaping. The userset's does
	// not — that id is FGA's own.
	if got := userPrefill("user"); got != "user:{{ fga_escape() }}" {
		t.Fatalf("plain = %q", got)
	}
}

func TestOtherLeavesTheFieldAlone(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldObject: "typed-by-hand"}))
	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+o"))
	selectByTitle(m.fieldPick, "Other…")
	send(m, key("enter"))
	if got := m.tupleForm.Values()[fieldObject]; got != "typed-by-hand" {
		t.Fatalf("object = %q", got)
	}
}

func TestWithoutAModelEveryFieldIsFreeText(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	send(m, key("esc"))
	m.sections.SetCursor(5)
	send(m, key("enter"), key("a"))

	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+o"))
	if m.fieldPick != nil {
		t.Fatal("no model means no picker")
	}
	typeText(m, "organization:1")
	if got := m.tupleForm.Values()[fieldObject]; got != "organization:1" {
		t.Fatalf("object = %q", got)
	}
}

func TestActionFieldIsHiddenWhenTheRuleSetsOne(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	if !m.tupleForm.Visible(int(fieldAction)) {
		t.Fatal("action should be offered when the rule has none")
	}
	send(m, key("esc"))
	m.rule().Action = "delete"
	m.openTuple(0)
	if m.tupleForm.Visible(int(fieldAction)) {
		t.Fatal("a rule-level action must hide the per-tuple one")
	}
}

func TestConditionIsHiddenForDeletes(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldAction: "delete"}))
	m.syncTupleVisibility()
	if m.tupleForm.Visible(int(fieldCondition)) {
		t.Fatal("delete has no condition")
	}
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldAction: "write"}))
	m.syncTupleVisibility()
	if !m.tupleForm.Visible(int(fieldCondition)) {
		t.Fatal("write may carry a condition")
	}
}

// A hidden field keeps its value so unhiding restores it, but it must not
// reach the document: mapper refuses `action: delete` alongside a condition,
// and reopening the tuple hides the field again, so the user could not clear it
// from the wizard.
func TestCommitDropsFieldsTheFormIsHiding(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{
		fieldObject:    "organization:acme",
		fieldRelation:  "member",
		fieldUser:      "user:1",
		fieldCondition: "in_region",
		fieldContext:   "region={{ input.data.object.region }}",
	}))

	m.tupleForm.FocusIndex(int(fieldAction))
	typeText(m, "delete")
	if m.tupleForm.Visible(int(fieldCondition)) || m.tupleForm.Visible(int(fieldContext)) {
		t.Fatal("a delete must hide the condition and its context")
	}
	send(m, key("esc"))

	got := (*m.tuples())[0]
	if got.Action != "delete" {
		t.Fatalf("action = %q, want delete", got.Action)
	}
	if got.Condition != "" || len(got.Context) != 0 {
		t.Fatalf("hidden fields reached the tuple: %+v", got)
	}
}

func TestCommitWritesTheTupleBack(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{
		fieldObject:   "organization:{{ input.data.object.organization.id }}",
		fieldRelation: "member",
		fieldUser:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		fieldWhen:     "input.data.object.user.user_id != \"\"",
	}))
	send(m, key("esc"))

	got := (*m.tuples())[0]
	if got.Object != "organization:{{ input.data.object.organization.id }}" {
		t.Fatalf("object = %q", got.Object)
	}
	if got.Relation != "member" || got.User == "" || got.When == "" {
		t.Fatalf("tuple = %+v", got)
	}
	if m.top() != screenTuples {
		t.Fatalf("top = %v", m.top())
	}
	if !strings.Contains(m.viewString(), "member") {
		t.Fatalf("the list should show the tuple:\n%s", m.viewString())
	}
}

func TestDeleteTuple(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"), key("esc"))
	send(m, key("a"), key("esc"))
	if got := len(*m.tuples()); got != 2 {
		t.Fatalf("tuples = %d", got)
	}
	send(m, key("d"))
	if got := len(*m.tuples()); got != 1 {
		t.Fatalf("tuples = %d, want 1", got)
	}
}

func TestConditionContextKeysComeFromTheModel(t *testing.T) {
	m := atTuples(t)
	m.index = mapping.IndexModel(conditionModel())
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldCondition: "in_region"}))
	m.syncTupleVisibility()

	keys := m.contextKeys()
	if len(keys) != 1 || keys[0] != "region" {
		t.Fatalf("context keys = %v", keys)
	}
}

func TestEditingATupleRefreshesThePreview(t *testing.T) {
	m := atTuples(t)
	// A rule needs a name to compile at all; atTuples leaves it unset (it never
	// visits the event picker, which is what normally fills it in), so this test
	// sets one directly rather than exercising that unrelated flow.
	m.rule().Name = "organization.member.added"
	m.rule().Sample = &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()}
	send(m, key("a"))
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{
		fieldObject:   "organization:{{ input.data.object.organization.id }}",
		fieldRelation: "member",
		fieldUser:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
	}))
	send(m, key("esc"))

	if len(m.preview.Tuples) != 1 {
		t.Fatalf("preview = %+v", m.preview)
	}
	if m.preview.Tuples[0].Relation != "member" {
		t.Fatalf("tuple = %+v", m.preview.Tuples[0])
	}
}

// tupleValues turns a sparse field map into the dense slice the form wants.
func tupleValues(set map[tupleField]string) []string {
	v := make([]string, fieldCount)
	for f, s := range set {
		v[f] = s
	}
	return v
}

func titles(p *picker.Picker) []string {
	var out []string
	for i := 0; i < p.Len(); i++ {
		p.SetCursor(i)
		out = append(out, p.Selected().Title)
	}
	p.SetCursor(0)
	return out
}

func selectByTitle(p *picker.Picker, title string) bool {
	for i := 0; i < p.Len(); i++ {
		p.SetCursor(i)
		if p.Selected().Title == title {
			return true
		}
	}
	p.SetCursor(0)
	return false
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// conditionModel is testModel plus a condition, for the context-key test.
func conditionModel() *openfga.AuthorizationModel {
	m := testModel()
	m.Conditions = map[string]openfga.Condition{
		"in_region": {
			Name:       "in_region",
			Expression: "params.region == \"eu\"",
			Parameters: map[string]openfga.ConditionParamType{
				"region": {TypeName: "TYPE_NAME_STRING"},
			},
		},
	}
	return m
}

// memberAddedEvent is the catalog sample, inlined so this file does not depend
// on the embedded copy.
func memberAddedEvent() map[string]any {
	return map[string]any{
		"type": "organization.member.added",
		"data": map[string]any{
			"object": map[string]any{
				"organization": map[string]any{"id": "org_1234567890abcdef"},
				"user":         map[string]any{"user_id": "auth0|507f1f77bcf86cd799439020"},
			},
		},
	}
}
