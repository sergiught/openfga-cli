package mapping

import (
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"
)

func TestShapeOf(t *testing.T) {
	tests := []struct {
		tuple language.Tuple
		want  Shape
	}{
		{language.Tuple{User: "user:anne", Relation: "member", Object: "organization:acme"}, Shape{"user", "member", "organization"}},
		{language.Tuple{User: "membership:o|u#holder", Relation: "assignee", Object: "role:o|r"}, Shape{"membership#holder", "assignee", "role"}},
		{language.Tuple{User: "user:*", Relation: "viewer", Object: "doc:1"}, Shape{"user:*", "viewer", "doc"}},
	}
	for _, tc := range tests {
		if got := ShapeOf(tc.tuple); got != tc.want {
			t.Errorf("ShapeOf(%+v) = %+v, want %+v", tc.tuple, got, tc.want)
		}
	}
	if got := (Shape{"user", "member", "organization"}).String(); got != "user member organization" {
		t.Errorf("String = %q", got)
	}
}

func TestRuleShapes(t *testing.T) {
	member := Tuple{User: "user:{{x}}", Relation: "member", Object: "organization:{{y}}"}
	tests := []struct {
		name         string
		rule         Rule
		want         []Shape
		wantUnshaped int
	}{
		{"deduplicated, iterator included", Rule{
			Tuples:   []Tuple{member, member},
			Iterator: &Iterator{Tuples: []Tuple{{User: "user:{{i}}", Relation: "holder", Object: "membership:{{y}}"}}},
		}, []Shape{{"user", "member", "organization"}, {"user", "holder", "membership"}}, 0},
		{"templated type and relation", Rule{Tuples: []Tuple{
			{User: "{{t}}:{{x}}", Relation: "member", Object: "organization:{{y}}"},
			{User: "user:{{x}}", Relation: "{{r}}", Object: "organization:{{y}}"},
		}}, nil, 2},
		{"delete tuple", Rule{Tuples: []Tuple{{User: "user:{{x}}", Relation: "member", Object: "organization:{{y}}", Action: "delete"}}}, nil, 0},
		{"delete rule", Rule{Action: "delete", Tuples: []Tuple{member}}, nil, 0},
		{"filters only", Rule{Filters: []TupleFilter{{Relation: "member"}}}, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, unshaped := RuleShapes(tc.rule)
			if !reflect.DeepEqual(got, tc.want) || unshaped != tc.wantUnshaped {
				t.Errorf("RuleShapes = %+v, %d; want %+v, %d", got, unshaped, tc.want, tc.wantUnshaped)
			}
		})
	}
}
