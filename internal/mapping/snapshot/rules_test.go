package snapshot

import (
	"reflect"
	"slices"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

var testTypes = []Type{
	{Name: "snapshot.organization.member", Mirrors: "organization.member.added"},
	{Name: "snapshot.organization.member.role", Mirrors: "organization.member.role.assigned"},
}

func whenDoc(whens ...string) *mapping.Document {
	doc := &mapping.Document{}
	for _, w := range whens {
		doc.Rules = append(doc.Rules, mapping.Rule{Name: w, When: w})
	}
	return doc
}

func TestRequested(t *testing.T) {
	doc := whenDoc(
		`input.type == "a" || input.type == "snapshot.organization.member"`,
		`input.type in ['x', 'snapshot.organization.member.role']`,
	)
	if got, want := Requested(doc, testTypes), []string{"snapshot.organization.member", "snapshot.organization.member.role"}; !slices.Equal(got, want) {
		t.Errorf("Requested() = %q, want %q", got, want)
	}

	onlyRole := whenDoc("input.type == `snapshot.organization.member.role`")
	if got, want := Requested(onlyRole, testTypes), []string{"snapshot.organization.member.role"}; !slices.Equal(got, want) {
		t.Errorf("Requested(onlyRole) = %q, want %q", got, want)
	}
}

func TestUnknownSnapshotTypes(t *testing.T) {
	doc := whenDoc(`input.type == "snapshot.organisation.member"`)
	if got, want := Unknown(doc, testTypes), []string{"snapshot.organisation.member"}; !slices.Equal(got, want) {
		t.Errorf("Unknown() = %q, want %q", got, want)
	}
	if got := Requested(doc, testTypes); len(got) != 0 {
		t.Errorf("Requested() = %q, want none", got)
	}
}

func TestUnknownIgnoresPrefixLiteral(t *testing.T) {
	doc := whenDoc(`input.type.startsWith("snapshot.") || input.type.startsWith("snapshot.organization.")`)
	if got := Unknown(doc, testTypes); len(got) != 0 {
		t.Errorf("Unknown() = %q, want none", got)
	}
	if IsSnapshotRule(doc.Rules[0], testTypes) {
		t.Error("IsSnapshotRule() = true, want false")
	}

	typo := whenDoc(`input.type.startsWith("snapshot.") || input.type == "snapshot.organisation."`)
	if got, want := Unknown(typo, testTypes), []string{"snapshot.organisation."}; !slices.Equal(got, want) {
		t.Errorf("Unknown(typo) = %q, want %q", got, want)
	}
}

func TestRuleShapesMixedDeleteAndWrite(t *testing.T) {
	r := mapping.Rule{Tuples: []mapping.Tuple{
		{User: "user:{{ a }}", Relation: "member", Object: "organization:{{ b }}", Action: "delete"},
		{User: "user:{{ a }}", Relation: "owner", Object: "organization:{{ b }}"},
	}}
	shapes, unshaped := mapping.RuleShapes(r)
	if want := []mapping.Shape{{UserType: "user", Relation: "owner", ObjectType: "organization"}}; !reflect.DeepEqual(shapes, want) {
		t.Errorf("shapes = %+v, want %+v", shapes, want)
	}
	if unshaped != 0 {
		t.Errorf("unshaped = %d, want 0", unshaped)
	}
}

func TestSuggestions(t *testing.T) {
	doc := whenDoc(`input.type == "organization.member.added"`)
	wantSuggestions := []Suggestion{{
		Rule:   `input.type == "organization.member.added"`,
		Clause: `|| input.type == "snapshot.organization.member"`,
	}}
	if got := Suggestions(doc, testTypes); !reflect.DeepEqual(got, wantSuggestions) {
		t.Errorf("Suggestions() = %+v, want %+v", got, wantSuggestions)
	}

	done := whenDoc(`input.type == "organization.member.added" || input.type == "snapshot.organization.member"`)
	if got := Suggestions(done, testTypes); len(got) != 0 {
		t.Errorf("Suggestions(done) = %+v, want none", got)
	}
}

func TestCoverage(t *testing.T) {
	member := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "member", Object: "organization:{{ input.data.org }}"}
	owner := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "owner", Object: "organization:{{ input.data.org }}"}
	identity := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "identity", Object: "connection:{{ input.data.conn }}"}
	templated := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "{{ input.data.role }}", Object: "organization:{{ input.data.org }}"}

	doc := &mapping.Document{Rules: []mapping.Rule{
		{Name: "members", When: `input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{member}},
		{Name: "gone", When: `input.type == "user.deleted"`, Action: "delete", Tuples: []mapping.Tuple{owner}},
		{Name: "identities", When: `input.type == "user.updated"`, Tuples: []mapping.Tuple{identity}},
		{Name: "roles", When: `input.type == "snapshot.organization.member.role"`, Tuples: []mapping.Tuple{templated}},
	}}

	owned, notChecked := Coverage(doc, testTypes)
	if want := []mapping.Shape{{UserType: "user", Relation: "member", ObjectType: "organization"}}; !reflect.DeepEqual(owned, want) {
		t.Errorf("owned = %+v, want %+v", owned, want)
	}
	wantNotChecked := []NotChecked{
		{
			Rule:   "identities",
			Reason: "no snapshot rule writes user identity connection",
			Shapes: []mapping.Shape{{UserType: "user", Relation: "identity", ObjectType: "connection"}},
		},
		{
			Rule:   "roles",
			Reason: "the type or relation is templated, so stale tuples cannot be told apart",
		},
	}
	if !reflect.DeepEqual(notChecked, wantNotChecked) {
		t.Errorf("notChecked = %+v, want %+v", notChecked, wantNotChecked)
	}
}

func TestOptInReadsTheExpression(t *testing.T) {
	const snap = "snapshot.organization.member"
	cases := []struct {
		name string
		when string
		want bool
	}{
		{"or clause", `input.type == "organization.member.added" || input.type == "snapshot.organization.member"`, true},
		{"line comment", "input.type == \"organization.member.added\"\n// || input.type == \"snapshot.organization.member\"\n", false},
		{"block comment", `input.type == "organization.member.added" /* || input.type == "snapshot.organization.member" */`, false},
		{"not equal", `input.type != "snapshot.organization.member"`, false},
		{"negated", `!(input.type == "snapshot.organization.member")`, false},
		{"in list", `input.type in ["organization.member.added", "snapshot.organization.member"]`, true},
		{"literal first", `"snapshot.organization.member" == input.type`, true},
		{"and clause", `input.type == "snapshot.organization.member" && input.data.object.x == "y"`, true},
		{"escaped quote before", `input.data.object.name == "a\"b" || input.type == "snapshot.organization.member"`, true},
		{"startsWith", `input.type.startsWith("snapshot.")`, false},
		{"optional chain", `input?.type == "snapshot.organization.member"`, true},
		{"does not parse", `input.type == "snapshot.organization.member" ||`, false},
	}
	for _, tc := range cases {
		r := mapping.Rule{Name: "r", When: tc.when}
		if got := IsSnapshotRule(r, testTypes); got != tc.want {
			t.Errorf("%s: IsSnapshotRule = %v, want %v", tc.name, got, tc.want)
		}
		var wantReq []string
		if tc.want {
			wantReq = []string{snap}
		}
		doc := &mapping.Document{Rules: []mapping.Rule{r}}
		if got := Requested(doc, testTypes); !slices.Equal(got, wantReq) {
			t.Errorf("%s: Requested = %v, want %v", tc.name, got, wantReq)
		}
		wantOwned := 0
		if tc.want {
			r.Tuples = []mapping.Tuple{{User: "user:{{ x }}", Relation: "member", Object: "organization:{{ y }}"}}
			wantOwned = 1
		}
		doc = &mapping.Document{Rules: []mapping.Rule{r}}
		if owned, _ := Coverage(doc, testTypes); len(owned) != wantOwned {
			t.Errorf("%s: owned = %v, want %d shapes", tc.name, owned, wantOwned)
		}
	}
	if got := Unknown(whenDoc(`input.type.startsWith("snapshot.")`), testTypes); len(got) != 0 {
		t.Errorf("Unknown(startsWith) = %v, want none", got)
	}
}

func TestCoverageTupleWhen(t *testing.T) {
	user := "user:{{ input.data.user_id }}"
	org := "organization:{{ input.data.org }}"
	rule := func(when string) mapping.Rule {
		return mapping.Rule{
			Name: "members",
			When: `input.type == "organization.member.added" || input.type == "snapshot.organization.member"`,
			Tuples: []mapping.Tuple{
				{User: user, Relation: "member", Object: org},
				{User: user, Relation: "owner", Object: org, When: when},
			},
		}
	}
	member := mapping.Shape{UserType: "user", Relation: "member", ObjectType: "organization"}
	owner := mapping.Shape{UserType: "user", Relation: "owner", ObjectType: "organization"}

	owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.type == "organization.member.added"`)}}, testTypes)
	if !reflect.DeepEqual(owned, []mapping.Shape{member}) {
		t.Errorf("owned = %+v, want only member", owned)
	}
	wantNC := []NotChecked{{
		Rule:   "members",
		Reason: "a tuple's own when leaves out snapshot events; not compared: user owner organization",
		Shapes: []mapping.Shape{owner},
	}}
	if !reflect.DeepEqual(notChecked, wantNC) {
		t.Errorf("notChecked = %+v, want %+v", notChecked, wantNC)
	}

	for name, when := range map[string]string{
		"no input.type":   `input.data.object.is_enabled`,
		"membership test": `"plan" in input.data.object.app_metadata`,
		"names snapshot":  `input.type == "snapshot.organization.member"`,
		"empty":           ``,
	} {
		owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{rule(when)}}, testTypes)
		if !reflect.DeepEqual(owned, []mapping.Shape{member, owner}) || len(notChecked) != 0 {
			t.Errorf("%s: owned = %+v, notChecked = %+v, want both shapes owned", name, owned, notChecked)
		}
	}

	owned, _ = Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.data.x ==`)}}, testTypes)
	if !reflect.DeepEqual(owned, []mapping.Shape{member}) {
		t.Errorf("unparsable tuple when: owned = %+v, want only member", owned)
	}

	// A snapshot type the rule itself never matches cannot fire the template.
	owned, _ = Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.type == "snapshot.organization.member.role"`)}}, testTypes)
	if !reflect.DeepEqual(owned, []mapping.Shape{member}) {
		t.Errorf("another snapshot type: owned = %+v, want only member", owned)
	}
}
