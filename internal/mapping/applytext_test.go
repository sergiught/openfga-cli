package mapping

import (
	"strings"
	"testing"

	"github.com/openfga/mapper/language"
)

func textPlan() *Plan {
	return &Plan{
		Events: []EventPlan{
			{Index: 3, Type: "member.added", Atomic: true, Changes: []language.Tuple{
				{User: "user:1", Relation: "member", Object: "org:acme", Condition: "c",
					Context: map[string]any{"b": "x", "a": float64(2)}},
			}},
			{Index: 4, Matched: false},
			{Index: 5, Atomic: false, Changes: []language.Tuple{
				{User: "user:1", Relation: "viewer", Object: "doc:1", Action: language.ActionDelete},
				{User: "user:1", Relation: "viewer", Object: "doc:1"},
			}},
		},
		Writes: 2, Deletes: 1, Unmatched: 1,
	}
}

func TestPlanTextMatchesTheCLI(t *testing.T) {
	got := PlanText(textPlan(), func(s string) string { return s })
	want := []string{
		"event 3 · member.added",
		`  + user:1 member org:acme (c: a=2, b="x")`,
		"event 5 (split into 2 requests)",
		"  - user:1 viewer doc:1",
		"  + user:1 viewer doc:1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlanSummaryAndQuestion(t *testing.T) {
	if got, want := PlanSummary(textPlan()), "3 events: 2 writes, 1 delete, 1 matched no rule"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		w, d     int
		name, id string
		want     string
	}{
		{2, 1, "acme", "01X", "Apply 2 writes and 1 delete to store acme (01X)?"},
		{1, 0, "acme", "01X", "Apply 1 write to store acme (01X)?"},
		{0, 3, "", "01X", "Apply 3 deletes to store 01X?"},
	} {
		if got := ApplyQuestion(tc.w, tc.d, tc.name, tc.id); got != tc.want {
			t.Errorf("question = %q, want %q", got, tc.want)
		}
	}
	if got, want := SentLine(textPlan(), "acme"), "sent 2 writes and 1 delete across 3 events to store acme"; got != want {
		t.Errorf("sent = %q, want %q", got, want)
	}
}

func TestAppliedSoFar(t *testing.T) {
	p := &Plan{Events: []EventPlan{{Index: 4}, {Index: 5}, {Index: 7}}}
	for applied, want := range []string{"no events applied", "applied event 4", "applied events 4-6"} {
		if got := AppliedSoFar(p, applied); got != want {
			t.Errorf("applied %d = %q, want %q", applied, got, want)
		}
	}
}
