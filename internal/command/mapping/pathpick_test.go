package mapping

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// atTupleWithSample opens a tuple on a rule that has the member-added sample.
func atTupleWithSample(t *testing.T) *wizardModel {
	t.Helper()
	m := atTuples(t)
	m.rule().Sample = &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()}
	send(m, key("a"))
	return m
}

func TestPathPickListsSamplePathsWithExamples(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+p"))
	if m.top() != screenPathPick {
		t.Fatalf("top = %v", m.top())
	}
	v := m.viewString()
	if !strings.Contains(v, "input.data.object.organization.id") {
		t.Fatalf("missing the path:\n%s", v)
	}
	if !strings.Contains(v, "org_1234567890abcdef") {
		t.Fatalf("missing the example value:\n%s", v)
	}
}

func TestPathPickInsertsATemplateIntoATemplateField(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldObject: "organization:"}))
	m.tupleForm.FocusIndex(int(fieldObject))
	m.tupleForm.SetCursor(int(fieldObject), len("organization:"))

	send(m, key("ctrl+p"))
	if !m.paths.SelectID("input.data.object.organization.id") {
		t.Fatal("path not offered")
	}
	send(m, key("enter"))

	want := "organization:{{ input.data.object.organization.id }}"
	if got := m.tupleForm.Values()[fieldObject]; got != want {
		t.Fatalf("object = %q, want %q", got, want)
	}
	if m.top() != screenTuple {
		t.Fatalf("top = %v", m.top())
	}
}

func TestPathPickInsertsABarePathIntoAnExpressionField(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldWhen: ""}))
	m.tupleForm.FocusIndex(int(fieldWhen))

	send(m, key("ctrl+p"))
	m.paths.SelectID("input.data.object.user.user_id")
	send(m, key("enter"))

	if got := m.tupleForm.Values()[fieldWhen]; got != "input.data.object.user.user_id" {
		t.Fatalf("when = %q", got)
	}
}

func TestPathPickInsertsAtTheCursorNotTheEnd(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldWhen: `x !=  and y`}))
	m.tupleForm.FocusIndex(int(fieldWhen))
	m.tupleForm.SetCursor(int(fieldWhen), len("x != "))

	send(m, key("ctrl+p"))
	m.paths.SelectID("input.data.object.user.user_id")
	send(m, key("enter"))

	want := `x != input.data.object.user.user_id and y`
	if got := m.tupleForm.Values()[fieldWhen]; got != want {
		t.Fatalf("when = %q, want %q", got, want)
	}
}

func TestPathPickIsFilterable(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.FocusIndex(int(fieldUser))
	send(m, key("ctrl+p"))

	send(m, key("/"))
	typeText(m, "user_id")

	visible := m.paths.Model.VisibleItems()
	if len(visible) == 0 {
		t.Fatal("filtering hid everything")
	}
	for _, raw := range visible {
		it, ok := raw.(uilist.Item)
		if !ok {
			t.Fatalf("unexpected item type %T", raw)
		}
		if !strings.Contains(it.ID, "user_id") {
			t.Fatalf("filter let through %q", it.ID)
		}
	}
}

func TestPathPickWithoutASampleExplainsItself(t *testing.T) {
	m := atTuples(t) // atTuples sets no sample
	send(m, key("a"))
	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+p"))

	if m.top() == screenPathPick {
		t.Fatal("there is nothing to pick without a sample")
	}
	if !strings.Contains(m.errMsg, "sample") {
		t.Fatalf("errMsg = %q", m.errMsg)
	}
}

func TestPathPickInIteratorScopeUsesTheAlias(t *testing.T) {
	m := atTuples(t)
	m.rule().Sample = &mapping.Sample{Label: "user.created", Event: userCreatedEvent()}
	m.rule().Iterator = &mapping.Iterator{
		Source: "input.data.object.identities",
		As:     "identity",
	}
	m.inIter = true
	m.openTuples()
	send(m, key("a"))
	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+p"))

	v := m.viewString()
	if !strings.Contains(v, "identity.connection") {
		t.Fatalf("iterator paths should be aliased:\n%s", v)
	}
	if strings.Contains(v, "input.data.object.identities[0].connection") {
		t.Fatalf("raw paths leaked into iterator scope:\n%s", v)
	}
}

func TestEscClosesThePathPickWithoutChanging(t *testing.T) {
	m := atTupleWithSample(t)
	m.tupleForm.SetValues(tupleValues(map[tupleField]string{fieldObject: "untouched"}))
	m.tupleForm.FocusIndex(int(fieldObject))
	send(m, key("ctrl+p"), key("esc"))

	if m.top() != screenTuple {
		t.Fatalf("top = %v", m.top())
	}
	if got := m.tupleForm.Values()[fieldObject]; got != "untouched" {
		t.Fatalf("object = %q", got)
	}
}

// userCreatedEvent has an array, for iterator-scope tests.
func userCreatedEvent() map[string]any {
	return map[string]any{
		"type": "user.created",
		"data": map[string]any{
			"object": map[string]any{
				"user_id": "auth0|507f1f77bcf86cd799439020",
				"identities": []any{
					map[string]any{
						"connection": "Username-Password-Authentication",
						"provider":   "auth0",
					},
				},
			},
		},
	}
}
