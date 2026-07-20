package modeltest

import (
	"fmt"
	"sort"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// idType returns the object/user type from an id like "document:1" or
// "user:anne" (the part before ':'), or the id unchanged when there's no ':'.
func idType(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[:i]
	}
	return id
}

// setDiff computes the unexpected (got-only) and missing (expected-only)
// elements between two sorted string sets.
func setDiff(expected, got []string) *SetDiff {
	sd := &SetDiff{}
	expSet := make(map[string]bool, len(expected))
	for _, e := range expected {
		expSet[e] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
		if !expSet[g] {
			sd.Unexpected = append(sd.Unexpected, g)
		}
	}
	for _, e := range expected {
		if !gotSet[e] {
			sd.Missing = append(sd.Missing, e)
		}
	}
	return sd
}

// sortedKeys returns the keys of m in ascending order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedSet returns a sorted, de-duplicated copy of s (an empty, non-nil slice
// when s is empty, so JSON marshaling is stable — [] rather than null).
// list_objects/list_users assertions compare as sets: a repeated element
// carries no meaning, so deduping here keeps the pass/fail decision (equalSets)
// consistent with the set-based diff (setDiff) — otherwise a duplicate in
// `expected` could fail an assertion whose diff shows nothing missing or extra.
func sortedSet(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// equalSets reports whether two sorted string slices contain the same
// elements.
func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// toProtoTuples converts modeltest tuple keys into the proto form Setup and
// Check expect.
func toProtoTuples(tuples []TupleKey) ([]*openfgav1.TupleKey, error) {
	out := make([]*openfgav1.TupleKey, 0, len(tuples))
	for _, tk := range tuples {
		pt := &openfgav1.TupleKey{User: tk.User, Relation: tk.Relation, Object: tk.Object}

		if tk.Condition != nil {
			cond := &openfgav1.RelationshipCondition{Name: tk.Condition.Name}
			if tk.Condition.Context != nil {
				s, err := structpb.NewStruct(tk.Condition.Context)
				if err != nil {
					return nil, fmt.Errorf("tuple %s %s %s: condition context: %w", tk.User, tk.Relation, tk.Object, err)
				}
				cond.Context = s
			}
			pt.Condition = cond
		}

		out = append(out, pt)
	}
	return out, nil
}
