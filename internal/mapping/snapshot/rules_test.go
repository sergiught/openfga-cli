package snapshot

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openfga/mapper/language"

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

	owned, notChecked := Coverage(doc, testTypes, nil, time.Time{})
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
		if owned, _ := Coverage(doc, testTypes, nil, time.Time{}); len(owned) != wantOwned {
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

	owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.type == "organization.member.added"`)}}, testTypes, nil, time.Time{})
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
		owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{rule(when)}}, testTypes, nil, time.Time{})
		if !reflect.DeepEqual(owned, []mapping.Shape{member, owner}) || len(notChecked) != 0 {
			t.Errorf("%s: owned = %+v, notChecked = %+v, want both shapes owned", name, owned, notChecked)
		}
	}

	owned, _ = Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.data.x ==`)}}, testTypes, nil, time.Time{})
	if !reflect.DeepEqual(owned, []mapping.Shape{member}) {
		t.Errorf("unparsable tuple when: owned = %+v, want only member", owned)
	}

	// A snapshot type the rule itself never matches cannot fire the template.
	owned, _ = Coverage(&mapping.Document{Rules: []mapping.Rule{rule(`input.type == "snapshot.organization.member.role"`)}}, testTypes, nil, time.Time{})
	if !reflect.DeepEqual(owned, []mapping.Shape{member}) {
		t.Errorf("another snapshot type: owned = %+v, want only member", owned)
	}
}

func TestCoverageWindowed(t *testing.T) {
	types := []Type{
		{Name: "snapshot.user", Windowed: true},
		{Name: "snapshot.organization.member"},
	}
	since := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	tup := func(rel, obj string) mapping.Tuple {
		return mapping.Tuple{User: "user:{{ input.user }}", Relation: rel, Object: obj + ":{{ input.id }}"}
	}
	userRule := mapping.Rule{Name: "users", When: `input.type == "snapshot.user"`, Tuples: []mapping.Tuple{tup("member", "tenant")}}
	memberRule := mapping.Rule{Name: "members", When: `input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{tup("member", "organization")}}
	bothRule := mapping.Rule{Name: "both", When: `input.type == "snapshot.user" || input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{tup("member", "tenant")}}
	otherRule := mapping.Rule{Name: "other", When: `input.type == "user.updated"`, Tuples: []mapping.Tuple{tup("member", "tenant")}}
	tenant := mapping.Shape{UserType: "user", Relation: "member", ObjectType: "tenant"}
	org := mapping.Shape{UserType: "user", Relation: "member", ObjectType: "organization"}
	reason := func(name string) string {
		return "read with --since: only " + name + " changed since 2026-11-24 09:00:00 UTC; not compared: user member tenant"
	}

	cases := []struct {
		name           string
		rules          []mapping.Rule
		wantOwned      []mapping.Shape
		wantNotChecked []NotChecked
	}{
		{"user rule leaves member rule owned", []mapping.Rule{userRule, memberRule}, []mapping.Shape{org},
			[]NotChecked{{Rule: "users", Reason: reason("snapshot.user"), Shapes: []mapping.Shape{tenant}}}},
		{"rule that opts in to both is windowed", []mapping.Rule{bothRule, memberRule}, []mapping.Shape{org},
			[]NotChecked{{Rule: "both", Reason: reason("snapshot.user"), Shapes: []mapping.Shape{tenant}}}},
		{"a whole-read rule writing the same shape does not own it", []mapping.Rule{userRule, {Name: "whole", When: `input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{tup("member", "tenant")}}}, nil,
			[]NotChecked{{Rule: "users", Reason: reason("snapshot.user"), Shapes: []mapping.Shape{tenant}}}},
		{"a non-snapshot rule on a windowed shape is not listed", []mapping.Rule{userRule, otherRule}, nil,
			[]NotChecked{{Rule: "users", Reason: reason("snapshot.user"), Shapes: []mapping.Shape{tenant}}}},
	}
	for _, tc := range cases {
		owned, notChecked := Coverage(&mapping.Document{Rules: tc.rules}, types, []string{"snapshot.user"}, since)
		if !reflect.DeepEqual(owned, tc.wantOwned) {
			t.Errorf("%s: owned = %+v, want %+v", tc.name, owned, tc.wantOwned)
		}
		if !reflect.DeepEqual(notChecked, tc.wantNotChecked) {
			t.Errorf("%s: notChecked = %+v, want %+v", tc.name, notChecked, tc.wantNotChecked)
		}
	}

	// Without a window the same rules are all owned, whatever since says.
	owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{userRule, memberRule}}, types, nil, since)
	if !reflect.DeepEqual(owned, []mapping.Shape{tenant, org}) || len(notChecked) != 0 {
		t.Errorf("no window: owned = %+v, notChecked = %+v", owned, notChecked)
	}
}

func TestCoverageWindowedEvents(t *testing.T) {
	types := []Type{
		{Name: "snapshot.user", Windowed: true},
		{Name: "snapshot.organization.member"},
	}
	since := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	tup := func(rel, obj string) mapping.Tuple {
		return mapping.Tuple{User: "user:{{ input.user }}", Relation: rel, Object: obj + ":{{ input.id }}"}
	}
	shape := func(rel, obj string) mapping.Shape {
		return mapping.Shape{UserType: "user", Relation: rel, ObjectType: obj}
	}
	const memberWhen = `input.type == "snapshot.organization.member"`
	whole := mapping.Rule{Name: "whole", When: memberWhen, Tuples: []mapping.Tuple{tup("member", "tenant")}}
	reason := func(shapes string) string {
		return "read with --since: only snapshot.user changed since 2026-11-24 09:00:00 UTC; not compared: " + shapes
	}
	member := shape("member", "tenant")
	admin := shape("admin", "tenant")

	cases := []struct {
		name           string
		rules          []mapping.Rule
		wantOwned      []mapping.Shape
		wantNotChecked []NotChecked
	}{
		{"a disjunct that does not read the type", []mapping.Rule{
			{Name: "admins", When: memberWhen + ` || input.admin == true`, Tuples: []mapping.Tuple{tup("admin", "tenant")}},
			{Name: "members", When: memberWhen, Tuples: []mapping.Tuple{tup("member", "organization")}},
		}, []mapping.Shape{shape("member", "organization")},
			[]NotChecked{{Rule: "admins", Reason: reason("user admin tenant"), Shapes: []mapping.Shape{admin}}}},
		{"a rule that never reads the type", []mapping.Rule{
			whole,
			{Name: "plain", When: `input.admin == true`, Tuples: []mapping.Tuple{tup("member", "tenant")}},
		}, nil,
			[]NotChecked{{Rule: "plain", Reason: reason("user member tenant"), Shapes: []mapping.Shape{member}}}},
		{"a rule with no when", []mapping.Rule{
			whole,
			{Name: "always", Tuples: []mapping.Tuple{tup("member", "tenant")}},
		}, nil,
			[]NotChecked{{Rule: "always", Reason: reason("user member tenant"), Shapes: []mapping.Shape{member}}}},
		{"rules limited to other event types", []mapping.Rule{
			whole,
			{Name: "eq", When: `input.type == "user.created"`, Tuples: []mapping.Tuple{tup("member", "tenant")}},
			{Name: "in", When: `input.type in ["user.created", "user.updated"]`, Tuples: []mapping.Tuple{tup("member", "tenant")}},
		}, []mapping.Shape{member}, nil},
		{"a conjunction with another type", []mapping.Rule{
			{Name: "conj", When: memberWhen + ` && input.admin == true`, Tuples: []mapping.Tuple{tup("member", "tenant")}},
		}, []mapping.Shape{member}, nil},
		{"a template limited to the whole-read type", []mapping.Rule{{
			Name: "both", When: `input.type == "snapshot.user" || ` + memberWhen,
			Tuples: []mapping.Tuple{
				{User: "user:{{ input.user }}", Relation: "member", Object: "tenant:{{ input.id }}", When: memberWhen},
				tup("admin", "tenant"),
			},
		}}, []mapping.Shape{member},
			[]NotChecked{{Rule: "both", Reason: reason("user admin tenant"), Shapes: []mapping.Shape{admin}}}},
		{"a templated relation owns nothing", []mapping.Rule{
			{Name: "users", When: `input.type == "snapshot.user"`, Tuples: []mapping.Tuple{
				{User: "user:{{ input.user }}", Relation: "{{ input.role }}", Object: "tenant:{{ input.id }}"},
			}},
			{Name: "members", When: memberWhen, Tuples: []mapping.Tuple{tup("member", "organization")}},
		}, nil,
			[]NotChecked{{Rule: "users", Reason: "read with --since: only snapshot.user changed since 2026-11-24 09:00:00 UTC, and the type or relation is templated, so no stale tuple can be told apart"}}},
	}
	for _, tc := range cases {
		owned, notChecked := Coverage(&mapping.Document{Rules: tc.rules}, types, []string{"snapshot.user"}, since)
		if !reflect.DeepEqual(owned, tc.wantOwned) {
			t.Errorf("%s: owned = %+v, want %+v", tc.name, owned, tc.wantOwned)
		}
		if !reflect.DeepEqual(notChecked, tc.wantNotChecked) {
			t.Errorf("%s: notChecked = %+v, want %+v", tc.name, notChecked, tc.wantNotChecked)
		}
		for _, n := range notChecked {
			if strings.HasSuffix(n.Reason, "not compared: ") {
				t.Errorf("%s: entry %q names no shapes", tc.name, n.Reason)
			}
		}
	}

	// Without a window the rules keep the owned shapes they always had.
	owned, notChecked := Coverage(&mapping.Document{Rules: []mapping.Rule{
		{Name: "admins", When: memberWhen + ` || input.admin == true`, Tuples: []mapping.Tuple{tup("admin", "tenant")}},
		whole,
		{Name: "plain", When: `input.admin == true`, Tuples: []mapping.Tuple{tup("member", "tenant")}},
		{Name: "viewers", When: `input.type == "user.created"`, Tuples: []mapping.Tuple{tup("viewer", "tenant")}},
	}}, types, nil, since)
	if want := []mapping.Shape{admin, member}; !reflect.DeepEqual(owned, want) {
		t.Errorf("no window: owned = %+v, want %+v", owned, want)
	}
	wantNC := []NotChecked{{Rule: "viewers", Reason: "no snapshot rule writes user viewer tenant", Shapes: []mapping.Shape{shape("viewer", "tenant")}}}
	if !reflect.DeepEqual(notChecked, wantNC) {
		t.Errorf("no window: notChecked = %+v, want %+v", notChecked, wantNC)
	}
}

func TestRemovers(t *testing.T) {
	const wantReason = "removes tuples that later events write again, and a snapshot does not say which happened last; not compared: "
	filter := []mapping.TupleFilter{{User: "user:{{ input.id }}", Object: "organization:", Action: "delete"}}
	byUser := `input.type == "snapshot.user"`
	member1 := tup("user:u1", "member", "organization:o1")
	member2 := tup("user:u2", "member", "organization:o1")
	viewer := tup("user:u1", "viewer", "organization:o1")
	team := tup("team:t1#member", "viewer", "organization:o2")

	t.Run("only the rule that fires on the type", func(t *testing.T) {
		doc := &mapping.Document{Rules: []mapping.Rule{
			{Name: "blocked", When: byUser, Filters: filter},
			{Name: "other", When: `input.type == "snapshot.organization"`, Action: "delete"},
			{Name: "plain", When: byUser, Tuples: []mapping.Tuple{{User: "a", Relation: "b", Object: "c:d"}}},
		}}
		got := Removers(doc, []Contested{{Tuple: member1, By: "snapshot.user"}})
		want := []NotChecked{{
			Rule:   "blocked",
			Reason: wantReason + "1 tuple of user member organization",
			Shapes: []mapping.Shape{mapping.ShapeOf(member1)},
			Tuples: []language.Tuple{member1},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Removers() = %+v, want %+v", got, want)
		}
	})

	t.Run("a delete template inside an iterator", func(t *testing.T) {
		doc := &mapping.Document{Rules: []mapping.Rule{{
			Name: "each", When: byUser,
			Iterator: &mapping.Iterator{Source: "input.xs", As: "x", Tuples: []mapping.Tuple{{User: "a", Relation: "b", Object: "c:d", Action: "delete"}}},
		}}}
		got := Removers(doc, []Contested{{Tuple: member1, By: "snapshot.user"}})
		if len(got) != 1 || got[0].Rule != "each" {
			t.Errorf("Removers() = %+v, want one entry for each", got)
		}
	})

	t.Run("three tuples over two shapes", func(t *testing.T) {
		doc := &mapping.Document{Rules: []mapping.Rule{{Name: "blocked", When: byUser, Filters: filter}}}
		got := Removers(doc, []Contested{
			{Tuple: member1, By: "snapshot.user"}, {Tuple: viewer, By: "snapshot.user"}, {Tuple: member2, By: "snapshot.user"},
		})
		want := wantReason + "3 tuples of user member organization, user viewer organization"
		if len(got) != 1 || got[0].Reason != want || len(got[0].Tuples) != 3 || len(got[0].Shapes) != 2 {
			t.Errorf("Removers() = %+v, want reason %q", got, want)
		}
	})

	t.Run("none", func(t *testing.T) {
		doc := &mapping.Document{Rules: []mapping.Rule{{Name: "blocked", When: byUser, Filters: filter}}}
		if got := Removers(doc, nil); got != nil {
			t.Errorf("Removers() = %+v, want nil", got)
		}
	})

	t.Run("unclaimed", func(t *testing.T) {
		doc := &mapping.Document{Rules: []mapping.Rule{{Name: "blocked", When: `input.type == "snapshot.other"`, Filters: filter}}}
		got := Removers(doc, []Contested{{Tuple: team, By: "snapshot.user"}})
		want := []NotChecked{{
			Rule:   "snapshot.user",
			Reason: wantReason + "1 tuple of team#member viewer organization",
			Shapes: []mapping.Shape{mapping.ShapeOf(team)},
			Tuples: []language.Tuple{team},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Removers() = %+v, want %+v", got, want)
		}
	})
}
