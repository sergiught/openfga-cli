package snapshot

import (
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// Tuples is a list read in order, possibly from disk.
type Tuples interface {
	// Len is how many tuples the list holds. A list that cannot count (a
	// disk read that failed) reports 0 and leaves the error to Each.
	Len() int
	// Each calls fn for each tuple in order and stops at the first error,
	// from fn or from reading the list.
	Each(fn func(language.Tuple) error) error
}

// Slice is a Tuples held in memory.
type Slice []language.Tuple

// Len implements Tuples.
func (s Slice) Len() int { return len(s) }

// Each implements Tuples.
func (s Slice) Each(fn func(language.Tuple) error) error {
	for _, t := range s {
		if err := fn(t); err != nil {
			return err
		}
	}
	return nil
}

// DiffLists is a Diff whose lists may live on disk. Changed and Was stay
// paired, index by index.
type DiffLists struct{ Missing, Changed, Was, Stale Tuples }

// Lists returns d's lists as Tuples.
func (d Diff) Lists() DiffLists {
	return DiffLists{Missing: Slice(d.Missing), Changed: Slice(d.Changed), Was: Slice(d.Was), Stale: Slice(d.Stale)}
}

// CountShapes is ByShape over a list: same order and counts.
func CountShapes(ts Tuples) ([]ShapeCount, error) {
	var out []ShapeCount
	idx := map[mapping.Shape]int{}
	err := ts.Each(func(t language.Tuple) error {
		s := mapping.ShapeOf(t)
		i, ok := idx[s]
		if !ok {
			i = len(out)
			idx[s] = i
			out = append(out, ShapeCount{Shape: s})
		}
		out[i].N++
		return nil
	})
	return out, err
}
