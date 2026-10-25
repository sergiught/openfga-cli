package tui

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

		{"ident ok", vIdent, "identity", false},
		{"ident underscore", vIdent, "_org2", false},
		{"ident leading digit", vIdent, "2org", true},
		{"ident dotted", vIdent, "org.id", true},
		{"ident spaced", vIdent, "my org", true},

		{"context pair", vContextPairs, "region={{ input.r }}", false},
		{"context comma in template", vContextPairs, "r={{ f(a,b) }}, s={{ c }}", false},
		{"context missing value", vContextPairs, "region=", true},
		{"context missing equals", vContextPairs, "region", true},
		// The commit cuts each pair on its first raw =, so the validator does
		// too: this one would commit the key "{{ a".
		{"context equals inside a templated key", vContextPairs, "{{ a == b }}k=v", true},
		{"context two pairs", vContextPairs, "a=1,b={{ y }}", false},
		{"context equals inside the value", vContextPairs, "ok={{ a == b }}", false},
		{"context key not an identifier", vContextPairs, "my key=1", true},

		{"filter object empty", vFilterObject, "", false},
		{"filter object type prefix", vFilterObject, "organization:", false},
		{"filter object full", vFilterObject, "organization:org_1", false},
		{"filter object templated id", vFilterObject, "organization:{{ input.data.object.id }}", false},
		{"filter object bare type", vFilterObject, "organization", true},
		{"filter object templated whole", vFilterObject, "{{ input.obj }}", false},
		// stripTemplates marks a template with "T", so a literal run of T's must
		// not be mistaken for one: it is as colonless as "organization".
		{"filter object literal T", vFilterObject, "T", true},
		{"filter object literal TTT", vFilterObject, "TTT", true},
		{"filter object literal T then template", vFilterObject, "T{{ x }}", true},
		{"filter object two templates no colon", vFilterObject, "{{ a }}{{ b }}", true},
		{"filter object two templates with colon", vFilterObject, "{{ a }}:{{ b }}", false},
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

// A blank filter object is a mapper validation error, not a wildcard, so the
// form must say so before the document is compiled.
func TestBlankFilterObjectIsFlaggedOnSubmit(t *testing.T) {
	if err := vFilterObject("organization"); err == nil {
		t.Fatal("a bare type with no colon should be rejected")
	} else if !strings.Contains(err.Error(), "type prefix") {
		t.Fatalf("message should name the type prefix, got %q", err)
	}
}

// An expected tuple's fields are the values FGA stores: no template, and a
// context in key=value pairs.
func TestExpectationValidatorsTakeLiteralsOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		fn   func(string) error
		in   string
		want string
	}{
		"object template":  {vLiteral(vObjectRef), "organization:{{ input.id }}", "an expectation is a literal value, not a template"},
		"user template":    {vLiteral(vUserRef), "{{ input.user }}", "an expectation is a literal value, not a template"},
		"context template": {vExpectedContext, "region={{ input.r }}", "an expectation is a literal value, not a template"},
		"context no value": {vExpectedContext, "region", "must be key=value, comma separated"},
		"object shape":     {vLiteral(vObjectRef), "organization", "must be type:id"},
		"required":         {vRequiredTuplePart(vLiteral(nil)), " ", "required"},
	} {
		if err := tc.fn(tc.in); err == nil || err.Error() != tc.want {
			t.Errorf("%s: %q gave %v, want %q", name, tc.in, err, tc.want)
		}
	}
	for _, ok := range []string{"", "region=eu", "region=eu, tier=gold"} {
		if err := vExpectedContext(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}
