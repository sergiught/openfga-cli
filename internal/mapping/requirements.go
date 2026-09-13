package mapping

// Requirement is one type-and-relation pair a recipe depends on, with the DSL
// fragment shown when the loaded model lacks it.
type Requirement struct {
	Type      string   // "organization"
	Relation  string   // "member"; empty means the type alone is required
	UserTypes []string // {"user"} — the directly-related user types
	DSL       string   // the fragment rendered in the gap panel
}

// RequirementStatus is what the loaded model has to say about one Requirement.
//
// Checked is false when there is no model to ask, which is a different answer
// from "missing": the wizard renders an unchecked status as a plain expectation
// rather than as a failure, since the user who skipped loading a model has not
// told us anything is wrong.
type RequirementStatus struct {
	Requirement Requirement
	TypeOK      bool
	RelationOK  bool
	UserTypesOK bool
	Checked     bool
}

// Satisfied reports whether the model covers this requirement. An unchecked
// status is never satisfied — absence of evidence is not evidence.
func (s RequirementStatus) Satisfied() bool {
	return s.Checked && s.TypeOK && s.RelationOK && s.UserTypesOK
}

// CheckRequirements asks the index whether each requirement holds. It reads
// exactly what lintAgainstModel reads, asked forwards ("will this work?")
// instead of backwards ("is this broken?").
//
// The checks cascade: a missing type makes the relation unanswerable, and a
// missing relation makes the user types unanswerable. Reporting those as
// separate failures would tell the user three things are wrong when one is.
func CheckRequirements(ix *ModelIndex, reqs []Requirement) []RequirementStatus {
	out := make([]RequirementStatus, 0, len(reqs))
	for _, r := range reqs {
		s := RequirementStatus{Requirement: r}
		if ix.Empty() {
			out = append(out, s)
			continue
		}
		s.Checked = true
		s.TypeOK = contains(ix.TypeNames(), r.Type)
		if !s.TypeOK {
			out = append(out, s)
			continue
		}
		if r.Relation == "" {
			// The recipe only needs the type to exist — a templated relation,
			// say, which no index can verify.
			s.RelationOK, s.UserTypesOK = true, true
			out = append(out, s)
			continue
		}
		s.RelationOK = contains(ix.RelationsFor(r.Type), r.Relation)
		if !s.RelationOK {
			out = append(out, s)
			continue
		}
		s.UserTypesOK = true
		allowed := ix.UserTypesFor(r.Type, r.Relation)
		for _, ut := range r.UserTypes {
			if !userTypeAllowed(allowed, ut) {
				s.UserTypesOK = false
				break
			}
		}
		out = append(out, s)
	}
	return out
}
