package mapping

import (
	"context"
	"strings"

	"github.com/openfga/mapper/language"
)

// PlannedChange is the last change planned for one user, relation and object.
type PlannedChange struct {
	Tuple language.Tuple // Action is language.ActionDelete for a planned delete
	Event int            // the 1-based index of the event that planned it
}

// PlanState holds the last planned change per user, relation and object.
type PlanState interface {
	Last(ctx context.Context, uro string) (PlannedChange, bool, error)
	// Plan records changes in order; a later change replaces an earlier one
	// for the same user, relation and object.
	Plan(ctx context.Context, changes []PlannedChange) error
	// Matching returns the planned changes, deletes included, whose tuple
	// matches f the way matchesFilter does. Order does not matter.
	Matching(ctx context.Context, f language.TupleFilter) ([]PlannedChange, error)
}

// mapState keeps the planned changes in memory, by object type: a read needs
// only the state of the object type it names.
type mapState struct {
	byType map[string]map[string]PlannedChange
}

// NewMapState returns a PlanState kept in memory.
func NewMapState() PlanState {
	return &mapState{byType: map[string]map[string]PlannedChange{}}
}

// objectType is the part of an object before its first colon.
func objectType(object string) string {
	typ, _, _ := strings.Cut(object, ":")
	return typ
}

func (s *mapState) Last(_ context.Context, uro string) (PlannedChange, bool, error) {
	object := uro[strings.LastIndexByte(uro, 0)+1:]
	pc, ok := s.byType[objectType(object)][uro]
	return pc, ok, nil
}

func (s *mapState) Plan(_ context.Context, changes []PlannedChange) error {
	for _, c := range changes {
		typ := objectType(c.Tuple.Object)
		if s.byType[typ] == nil {
			s.byType[typ] = map[string]PlannedChange{}
		}
		s.byType[typ][URO(c.Tuple)] = c
	}
	return nil
}

func (s *mapState) Matching(_ context.Context, f language.TupleFilter) ([]PlannedChange, error) {
	var out []PlannedChange
	collect := func(bucket map[string]PlannedChange) {
		for _, pc := range bucket {
			if matchesFilter(pc.Tuple, f) {
				out = append(out, pc)
			}
		}
	}
	if f.Object != "" {
		collect(s.byType[objectType(f.Object)])
	} else {
		for _, bucket := range s.byType {
			collect(bucket)
		}
	}
	return out, nil
}
