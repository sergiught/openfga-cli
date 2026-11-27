package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

func streamTuples(n int) []language.Tuple {
	all := []language.Tuple{
		{User: "user:<anne>", Relation: "member", Object: "organization:a&b"},
		{User: "user:émile", Relation: "viewer", Object: "doc:日本", Condition: "in_window", Context: map[string]any{"from": "2026-01-01", "n": float64(3), "tags": []any{"x", "y"}, "empty": map[string]any{}}},
		{User: "group:g#member", Relation: "viewer", Object: "doc:>1", Condition: "c"},
	}
	return all[:n]
}

func setCompact(t *testing.T, v bool) {
	t.Helper()
	old := output.Compact
	t.Cleanup(func() { output.Compact = old })
	output.Compact = v
}

func TestStreamJSONMatchesOutputJSON(t *testing.T) {
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	since := at.Add(-time.Hour)
	meta := snapshotMeta{Store: "01H", Provider: "auth0", Source: "example.us.auth0.com", StartedAt: at, FinishedAt: at,
		Counts: []snapshotCount{{Type: "snapshot.user", Count: 2}}, Since: &since}
	nc := []notCheckedOut{{Rule: "r", Reason: "because <&>", Shapes: []string{"user#x"}, Tuples: tuplesOut(streamTuples(2))}}

	for _, compact := range []bool{false, true} {
		for _, n := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("compact=%v/n=%d", compact, n), func(t *testing.T) {
				setCompact(t, compact)
				ts := streamTuples(n)

				b := backfillResult{snapshotMeta: meta, StoreReadAt: at, Tuples: tuplesOut(ts), Missing: tuplesOut(ts), Changed: tuplesOut(ts[:n/2]),
					Existing: 4, NotChecked: nc, Sent: sentOut{Writes: 2}}
				var want, got bytes.Buffer
				if err := output.JSON(&want, b); err != nil {
					t.Fatal(err)
				}
				stub := b
				stub.Tuples, stub.Missing, stub.Changed = nil, nil, nil
				if err := streamJSON(&got, stub, map[string]snapshot.Tuples{
					"tuples": snapshot.Slice(ts), "missing": snapshot.Slice(ts), "changed": snapshot.Slice(ts[:n/2])}); err != nil {
					t.Fatal(err)
				}
				if want.String() != got.String() {
					t.Fatalf("streamed:\n%s\nwant:\n%s", got.String(), want.String())
				}

				d := driftResult{snapshotMeta: meta, StoreReadAt: at, Missing: tuplesOut(ts), Changed: tuplesOut(ts[:n/2]), Stale: tuplesOut(nil),
					NotChecked: notCheckedOuts(nil), Pruned: 1}
				want.Reset()
				got.Reset()
				if err := output.JSON(&want, d); err != nil {
					t.Fatal(err)
				}
				stub2 := d
				stub2.Missing, stub2.Changed, stub2.Stale = nil, nil, nil
				// A nil list renders like an empty one.
				if err := streamJSON(&got, stub2, map[string]snapshot.Tuples{
					"missing": snapshot.Slice(ts), "changed": snapshot.Slice(ts[:n/2]), "stale": nil}); err != nil {
					t.Fatal(err)
				}
				if want.String() != got.String() {
					t.Fatalf("streamed:\n%s\nwant:\n%s", got.String(), want.String())
				}
			})
		}
	}
}

func TestStreamDocIndentsNestedLists(t *testing.T) {
	type entry struct {
		Name string          `json:"name"`
		L    json.RawMessage `json:"l"`
	}
	type plainEntry struct {
		Name string     `json:"name"`
		L    []tupleOut `json:"l"`
	}
	for _, compact := range []bool{false, true} {
		setCompact(t, compact)
		ts := streamTuples(3)
		doc := struct {
			Shapes []entry `json:"shapes"`
		}{[]entry{{"a", sentinel("a")}, {"b", sentinel("b")}}}
		plain := struct {
			Shapes []plainEntry `json:"shapes"`
		}{[]plainEntry{{"a", tuplesOut(ts)}, {"b", tuplesOut(nil)}}}
		var want, got bytes.Buffer
		if err := output.JSON(&want, plain); err != nil {
			t.Fatal(err)
		}
		if err := streamDoc(&got, doc, map[string]snapshot.Tuples{"a": snapshot.Slice(ts), "b": snapshot.Slice{}}); err != nil {
			t.Fatal(err)
		}
		if want.String() != got.String() {
			t.Fatalf("streamed:\n%s\nwant:\n%s", got.String(), want.String())
		}
	}
}

func TestStreamJSONRefusesACollidingSentinel(t *testing.T) {
	setCompact(t, false)
	stub := driftResult{snapshotMeta: snapshotMeta{Store: "\x00LIST:missing\x00"}}
	var got bytes.Buffer
	err := streamJSON(&got, stub, map[string]snapshot.Tuples{"missing": snapshot.Slice(streamTuples(1)), "changed": nil, "stale": nil})
	if err == nil {
		t.Error("a colliding sentinel was accepted")
	}
	if got.Len() != 0 {
		t.Errorf("output = %q, want none", got.String())
	}

	// A list the document has no field for is an error too.
	if err = streamJSON(&got, driftResult{}, map[string]snapshot.Tuples{"nope": nil}); err == nil {
		t.Error("an unknown list was accepted")
	}
}

func TestStreamJSONReportsListErrors(t *testing.T) {
	setCompact(t, false)
	var got bytes.Buffer
	err := streamJSON(&got, driftResult{}, map[string]snapshot.Tuples{"missing": failing{}, "changed": nil, "stale": nil})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want it to say boom", err)
	}
}

type failing struct{}

func (failing) Len() int { return 1 }
func (failing) Each(func(language.Tuple) error) error {
	return fmt.Errorf("boom")
}
