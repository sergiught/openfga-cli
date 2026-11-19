package snapshot

import (
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// Diff is how a store differs from the desired tuples. Changed holds the
// desired version. All three are sorted like Desired.
type Diff struct{ Missing, Changed, Stale []language.Tuple }

// Empty reports whether the store already matches.
func (d Diff) Empty() bool {
	return len(d.Missing) == 0 && len(d.Changed) == 0 && len(d.Stale) == 0
}

// Compare diffs desired against store. A tuple's identity is user, relation
// and object; one with the same identity but another condition or context is
// Changed. A store tuple absent from desired is Stale only when its shape is
// in owned: the mapping manages that shape, anything else is left alone.
func Compare(desired, store []language.Tuple, owned []mapping.Shape) Diff {
	own := make(map[mapping.Shape]bool, len(owned))
	for _, s := range owned {
		own[s] = true
	}
	inStore := make(map[string]language.Tuple, len(store))
	for _, t := range store {
		inStore[identity(t)] = t
	}
	var d Diff
	seen := make(map[string]bool, len(desired))
	for _, t := range desired {
		k := identity(t)
		seen[k] = true
		s, ok := inStore[k]
		switch {
		case !ok:
			d.Missing = append(d.Missing, t)
		case plain(s).Key() != plain(t).Key():
			d.Changed = append(d.Changed, t)
		}
	}
	for _, t := range store {
		if !seen[identity(t)] && own[mapping.ShapeOf(t)] {
			d.Stale = append(d.Stale, t)
		}
	}
	sortTuples(d.Missing)
	sortTuples(d.Changed)
	sortTuples(d.Stale)
	return d
}

// plain clears the action so Key compares only the tuple itself.
func plain(t language.Tuple) language.Tuple {
	t.Action = ""
	return t
}

// ShapeCount is how many tuples have one shape.
type ShapeCount struct {
	Shape mapping.Shape
	N     int
}

// ByShape counts tuples per shape in first-seen order of the input.
func ByShape(ts []language.Tuple) []ShapeCount {
	var out []ShapeCount
	idx := map[mapping.Shape]int{}
	for _, t := range ts {
		s := mapping.ShapeOf(t)
		i, ok := idx[s]
		if !ok {
			i = len(out)
			idx[s] = i
			out = append(out, ShapeCount{Shape: s})
		}
		out[i].N++
	}
	return out
}
