package snapshot

import (
	"slices"
	"strings"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// literals returns the contents of every quoted string literal in a "when"
// expression, delimited by ", ' or ` on both sides.
func literals(when string) []string {
	var out []string
	for i := 0; i < len(when); i++ {
		q := when[i]
		if q != '"' && q != '\'' && q != '`' {
			continue
		}
		end := strings.IndexByte(when[i+1:], q)
		if end < 0 {
			break
		}
		out = append(out, when[i+1:i+1+end])
		i += end + 1
	}
	return out
}

func names(r mapping.Rule, name string) bool {
	return slices.Contains(literals(r.When), name)
}

// IsSnapshotRule reports whether r's "when" names any of types.
func IsSnapshotRule(r mapping.Rule, types []Type) bool {
	for _, t := range types {
		if names(r, t.Name) {
			return true
		}
	}
	return false
}

// Requested returns the types some rule names, in types' order.
func Requested(doc *mapping.Document, types []Type) []string {
	var out []string
	for _, t := range types {
		for _, r := range doc.Rules {
			if names(r, t.Name) {
				out = append(out, t.Name)
				break
			}
		}
	}
	return out
}

// Unknown returns the quoted literals starting "snapshot." that no type
// names, sorted and deduplicated.
func Unknown(doc *mapping.Document, types []Type) []string {
	var out []string
	for _, r := range doc.Rules {
		for _, lit := range literals(r.When) {
			if !strings.HasPrefix(lit, "snapshot.") || slices.Contains(out, lit) {
				continue
			}
			if !slices.ContainsFunc(types, func(t Type) bool { return t.Name == lit }) {
				out = append(out, lit)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Suggestion is a rule that handles a live event but not its snapshot twin.
type Suggestion struct{ Rule, Clause string }

// Suggestions lists the non-snapshot rules whose "when" names a type's
// Mirrors event, with the clause that would opt them in.
func Suggestions(doc *mapping.Document, types []Type) []Suggestion {
	var out []Suggestion
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) {
			continue
		}
		for _, t := range types {
			if names(r, t.Mirrors) {
				out = append(out, Suggestion{Rule: r.Name, Clause: `|| input.type == "` + t.Name + `"`})
			}
		}
	}
	return out
}

// NotChecked is a rule whose tuples a snapshot cannot vouch for.
type NotChecked struct {
	Rule   string
	Reason string
	Shapes []mapping.Shape
}

// Coverage returns the shapes snapshot rules write, and the rules whose
// tuples fall outside that: non-snapshot rules writing shapes no snapshot
// rule owns, and snapshot rules with templated types or relations.
func Coverage(doc *mapping.Document, types []Type) (owned []mapping.Shape, notChecked []NotChecked) {
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) {
			shapes, _ := mapping.RuleShapes(r)
			for _, s := range shapes {
				if !slices.Contains(owned, s) {
					owned = append(owned, s)
				}
			}
		}
	}
	for _, r := range doc.Rules {
		shapes, unshaped := mapping.RuleShapes(r)
		if IsSnapshotRule(r, types) {
			if unshaped > 0 {
				notChecked = append(notChecked, NotChecked{
					Rule:   r.Name,
					Reason: "the type or relation is templated, so stale tuples cannot be told apart",
				})
			}
			continue
		}
		var missing []mapping.Shape
		var text []string
		for _, s := range shapes {
			if !slices.Contains(owned, s) {
				missing = append(missing, s)
				text = append(text, s.String())
			}
		}
		if len(missing) > 0 {
			notChecked = append(notChecked, NotChecked{
				Rule:   r.Name,
				Reason: "no snapshot rule writes " + strings.Join(text, ", "),
				Shapes: missing,
			})
		}
	}
	return owned, notChecked
}
