package mapping

import (
	"strings"
	"testing"
)

func TestValidatorsCheckShapeAndStayQuietOnEmpty(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) error
		in   string
		bad  bool
	}{
		{"object empty", vObjectRef, "", false},
		{"object templated id", vObjectRef, "organization:{{ input.data.id }}", false},
		{"object templated type", vObjectRef, "{{ input.type }}:{{ input.id }}", false},
		{"object colon inside template", vObjectRef, `org:{{ x == "a:b" ? y : z }}`, false},
		{"object missing id", vObjectRef, "organization", true},
		{"object empty type", vObjectRef, ":{{ input.id }}", true},
		{"object wildcard", vObjectRef, "user:*", true},
		{"object userset", vObjectRef, "team:eng#member", true},

		{"user wildcard", vUserRef, "user:*", false},
		{"user userset", vUserRef, "team:eng#member", false},
		{"user missing id", vUserRef, "user", true},

		{"unclosed template", vTemplate, "user:{{ input.id", true},
		{"stray close", vTemplate, "user: input.id }}", true},
		{"balanced", vTemplate, "{{ a }}-{{ b }}", false},

		{"tuple action write", vTupleAction, "write", false},
		{"tuple action patch", vTupleAction, "patch", true},
		{"filter action patch", vFilterAction, "patch", false},
		{"filter action write", vFilterAction, "write", true},
		{"action templated", vTupleAction, "{{ input.op }}", false},

		{"ident ok", vIdent, "identity", false},
		{"ident underscore", vIdent, "_org2", false},
		{"ident leading digit", vIdent, "2org", true},
		{"ident dotted", vIdent, "org.id", true},
		{"ident spaced", vIdent, "my org", true},

		{"context pair", vContextPairs, "region={{ input.r }}", false},
		{"context comma in template", vContextPairs, "r={{ f(a,b) }}, s={{ c }}", false},
		{"context missing value", vContextPairs, "region=", true},
		{"context missing equals", vContextPairs, "region", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn(tc.in)
			if tc.bad && err == nil {
				t.Fatalf("%q should have been rejected", tc.in)
			}
			if !tc.bad && err != nil {
				t.Fatalf("%q rejected: %v", tc.in, err)
			}
		})
	}
}

// atTupleForm opens the first tuple of a rule, ready to type into.
func atTupleForm(t *testing.T) *wizardModel {
	t.Helper()
	m := atTuples(t)
	send(m, key("a"))
	if m.top() != screenTuple {
		t.Fatalf("top = %v, want the tuple form", m.top())
	}
	return m
}

func TestTypingInAFormUpdatesThePreviewWithoutLeavingIt(t *testing.T) {
	m := atTupleForm(t)
	typeText(m, "organization:acme")

	// The preview pane is the whole point of the layout; it must not wait for
	// the user to back out of the form.
	if got := (*m.tuples())[0].Object; got != "organization:acme" {
		t.Fatalf("object not committed while typing: %q", got)
	}
	if !strings.Contains(string(m.preview.YAML), "organization:acme") {
		t.Fatalf("preview did not follow the keystrokes:\n%s", m.preview.YAML)
	}
	if m.top() != screenTuple {
		t.Fatalf("typing should not leave the form: top = %v", m.top())
	}
}

func TestLintProblemsAppearWhileStillInTheForm(t *testing.T) {
	m := atTupleForm(t)
	// "organisation" is not in the test model, which knows only user and
	// organization. Lint should say so before the user leaves the screen.
	typeText(m, "organisation:acme")

	var found bool
	for _, p := range m.problems {
		if p.Field == "object" && strings.Contains(p.Message, "organisation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unknown type was not reported while editing: %+v", m.problems)
	}
}

func TestTabbingOffABadValueFlagsItInline(t *testing.T) {
	m := atTupleForm(t)
	typeText(m, "organization") // no id half
	send(m, key("tab"))         // blur validates

	if !strings.Contains(m.viewString(), "must be type:id") {
		t.Fatalf("no inline error after tabbing off a bad object:\n%s", m.viewString())
	}
}
