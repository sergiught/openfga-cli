package mapping

import (
	"fmt"
	"strings"
)

// Problem is one thing wrong with a document. Blocking problems stop a save;
// warnings are advisory — the model is a snapshot and a mapping may legitimately
// target types a newer model will add.
type Problem struct {
	Rule    int
	Section string
	Field   string
	Message string
	Warning bool
}

// Blocking filters ps down to the problems that must be fixed before saving.
func Blocking(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if !p.Warning {
			out = append(out, p)
		}
	}
	return out
}

// Warnings filters ps down to the advisory ones — every check that compared the
// document against a model. They are the complement of Blocking.
func Warnings(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Warning {
			out = append(out, p)
		}
	}
	return out
}

// Lint checks d for the mistakes mapper cannot catch: structurally incomplete
// rules the wizard allows mid-edit, and identifiers that compile but do not
// exist in ix. A nil ix disables the model checks entirely.
func Lint(d *Document, ix *ModelIndex) []Problem {
	var ps []Problem
	if len(d.Rules) == 0 {
		return append(ps, Problem{Rule: -1, Section: "rule", Message: "a mapping needs at least one rule"})
	}

	seen := map[string]bool{}
	for i, r := range d.Rules {
		switch {
		case strings.TrimSpace(r.Name) == "":
			ps = append(ps, Problem{Rule: i, Section: "rule", Field: "name", Message: "every rule needs a name"})
		case seen[r.Name]:
			ps = append(ps, Problem{Rule: i, Section: "rule", Field: "name",
				Message: fmt.Sprintf("duplicate rule name %q", r.Name)})
		default:
			seen[r.Name] = true
		}

		ps = append(ps, lintTuples(i, r, ix)...)
		ps = append(ps, lintVariables(i, r)...)
		ps = append(ps, lintIterator(i, r)...)
		ps = append(ps, lintFilters(i, r)...)
	}
	return ps
}

func lintTuples(i int, r Rule, ix *ModelIndex) []Problem {
	var ps []Problem
	all := r.Tuples
	if r.Iterator != nil {
		all = append(append([]Tuple{}, all...), r.Iterator.Tuples...)
	}
	if len(all) == 0 {
		// A rule can do its whole job through tuple_filters — that is what the
		// deletion events map to — so only a rule that does nothing at all is
		// missing something.
		if len(r.Filters) > 0 {
			return ps
		}
		return append(ps, Problem{Rule: i, Section: "tuple",
			Message: "rule has no tuples yet"})
	}
	for _, t := range all {
		for _, f := range []struct{ name, val string }{
			{"user", t.User}, {"relation", t.Relation}, {"object", t.Object},
		} {
			if strings.TrimSpace(f.val) == "" {
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: f.name,
					Message: fmt.Sprintf("tuple is missing its %s", f.name)})
			}
		}
		if r.Action != "" && t.Action != "" {
			ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "action",
				Message: "a tuple action cannot be combined with a rule-level action"})
		}
		ps = append(ps, lintAgainstModel(i, t, ix)...)
	}
	return ps
}

// lintAgainstModel checks the parts of a tuple that resolve statically. Anything
// containing `{{` is only known at evaluation time and is left alone.
func lintAgainstModel(i int, t Tuple, ix *ModelIndex) []Problem {
	if ix.Empty() {
		return nil
	}
	var ps []Problem

	typ, literalType := literalObjectType(t.Object)
	if literalType && typ != "" && !contains(ix.TypeNames(), typ) {
		ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "object", Warning: true,
			Message: fmt.Sprintf("the model has no type %q", typ)})
	}

	relationKnown := false
	if literalType && typ != "" && !strings.Contains(t.Relation, "{{") && t.Relation != "" {
		if contains(ix.TypeNames(), typ) {
			if contains(ix.RelationsFor(typ), t.Relation) {
				relationKnown = true
			} else {
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "relation", Warning: true,
					Message: fmt.Sprintf("type %q has no relation %q", typ, t.Relation)})
			}
		}
	}
	if relationKnown {
		if userTyp, literalUser := literalUserType(t.User); literalUser {
			if userTypes := ix.UserTypesFor(typ, t.Relation); len(userTypes) > 0 && !userTypeAllowed(userTypes, userTyp) {
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "user", Warning: true,
					Message: fmt.Sprintf("relation %q on type %q does not accept user type %q", t.Relation, typ, userTyp)})
			}
		}
	}

	if t.Condition != "" {
		params := ix.ConditionParams(t.Condition)
		if params == nil && !contains(ix.ConditionNames(), t.Condition) {
			ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "condition", Warning: true,
				Message: fmt.Sprintf("the model has no condition %q", t.Condition)})
		} else {
			set := map[string]bool{}
			for _, e := range t.Context {
				set[e.Key] = true
			}
			var missing []string
			for _, p := range params {
				if !set[p] {
					missing = append(missing, p)
				}
			}
			if len(missing) > 0 {
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "context", Warning: true,
					Message: fmt.Sprintf("condition %q has no value for %s", t.Condition, strings.Join(missing, ", "))})
			}
			for _, e := range t.Context {
				if !contains(params, e.Key) {
					ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "context", Warning: true,
						Message: fmt.Sprintf("condition %q has no parameter %q", t.Condition, e.Key)})
				}
			}
		}
	}
	return ps
}

// literalObjectType extracts the type from `type:id`, reporting false when the
// type half is templated and therefore unknowable until evaluation.
func literalObjectType(object string) (string, bool) {
	colon := strings.IndexByte(object, ':')
	if colon <= 0 {
		return "", false
	}
	typ := object[:colon]
	if strings.Contains(typ, "{{") {
		return "", false
	}
	return typ, true
}

// literalUserType extracts the reference form a tuple's user contributes to a
// model check: "user" for "user:123", or "group#member" for a userset like
// "group:eng#member". Reports false when the type half is templated and
// therefore unknowable until evaluation.
func literalUserType(user string) (string, bool) {
	colon := strings.IndexByte(user, ':')
	if colon <= 0 {
		return "", false
	}
	typ := user[:colon]
	if strings.Contains(typ, "{{") {
		return "", false
	}
	rest := user[colon+1:]
	if hash := strings.IndexByte(rest, '#'); hash >= 0 {
		relation := rest[hash+1:]
		if strings.Contains(relation, "{{") {
			return "", false
		}
		return typ + "#" + relation, true
	}
	return typ, true
}

// userTypeAllowed reports whether userTyp is one of the reference strings a
// relation accepts. A "type:*" wildcard entry also permits a direct user of
// that type.
func userTypeAllowed(userTypes []string, userTyp string) bool {
	if contains(userTypes, userTyp) {
		return true
	}
	return !strings.Contains(userTyp, "#") && contains(userTypes, userTyp+":*")
}

func lintVariables(i int, r Rule) []Problem {
	var ps []Problem
	for _, v := range r.Variables {
		if strings.TrimSpace(v.Name) == "" {
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "name",
				Message: "every variable needs a name"})
		}
		if strings.TrimSpace(v.Expr) == "" {
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "expression",
				Message: fmt.Sprintf("variable %q needs an expression", v.Name)})
		}
	}
	return ps
}

func lintIterator(i int, r Rule) []Problem {
	if r.Iterator == nil {
		return nil
	}
	var ps []Problem
	if strings.TrimSpace(r.Iterator.Source) == "" {
		ps = append(ps, Problem{Rule: i, Section: "iterator", Field: "source",
			Message: "the iterator needs a source expression"})
	}
	if strings.TrimSpace(r.Iterator.As) == "" {
		ps = append(ps, Problem{Rule: i, Section: "iterator", Field: "as",
			Message: "the iterator needs an alias"})
	}
	return ps
}

func lintFilters(i int, r Rule) []Problem {
	var ps []Problem
	for _, f := range r.Filters {
		if strings.TrimSpace(f.Object) == "" {
			ps = append(ps, Problem{Rule: i, Section: "filters", Field: "object",
				Message: "a tuple filter needs an object"})
		}
		// A filter with no action is a patch, and a patch is a reconciliation:
		// mapper diffs the tuples the rule produced against the ones the filter
		// matches. A rule that produces none has an empty desired state, which
		// mapper refuses outright rather than read as "delete everything" — and
		// a failed event stops the whole pipeline. The mistake is invisible
		// until the first event arrives, so it is caught here instead.
		if f.Action == "" && !r.WritesTuples() {
			ps = append(ps, Problem{Rule: i, Section: "filters", Field: "action",
				Message: "this filter patches, but the rule writes no tuples for it to " +
					"reconcile against — mapper fails the event. Add tuples, or make it a delete"})
		}
	}
	return ps
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
