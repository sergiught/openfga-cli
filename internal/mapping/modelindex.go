package mapping

import (
	"sort"

	"github.com/sergiught/go-openfga/openfga"
)

// ModelIndex is the authorization model reduced to what the tuple editor needs:
// which types exist, which relations each has, which user types a relation
// accepts, and which conditions can be attached.
//
// Every accessor tolerates a nil receiver, because "skip the model" is a first
// class choice in the wizard and the editors should not each re-check.
type ModelIndex struct {
	Types      map[string][]string
	UserTypes  map[string][]string
	Conditions map[string][]string
}

// IndexModel builds an index from a fetched or loaded model. A nil model
// indexes to an empty, still-usable index.
func IndexModel(m *openfga.AuthorizationModel) *ModelIndex {
	ix := &ModelIndex{
		Types:      map[string][]string{},
		UserTypes:  map[string][]string{},
		Conditions: map[string][]string{},
	}
	if m == nil {
		return ix
	}

	for _, td := range m.TypeDefinitions {
		relations := make([]string, 0, len(td.Relations))
		for rel := range td.Relations {
			relations = append(relations, rel)
		}
		sort.Strings(relations)
		ix.Types[td.Type] = relations

		if td.Metadata == nil {
			continue
		}
		for rel, meta := range td.Metadata.Relations {
			seen := map[string]bool{}
			var users []string
			for _, ref := range meta.DirectlyRelatedUserTypes {
				u := renderUserRef(ref)
				if u == "" || seen[u] {
					continue
				}
				seen[u] = true
				users = append(users, u)
			}
			sort.Strings(users)
			ix.UserTypes[td.Type+"#"+rel] = users
		}
	}

	for name, cond := range m.Conditions {
		params := make([]string, 0, len(cond.Parameters))
		for p := range cond.Parameters {
			params = append(params, p)
		}
		sort.Strings(params)
		ix.Conditions[name] = params
	}

	return ix
}

// renderUserRef turns a relation reference into the user string a tuple would
// carry: "user", "group#member" or "user:*". The reference's condition is
// deliberately dropped — the wizard picks a tuple's condition separately, and
// folding it in here would offer "user (in_business_hours)" as a user type.
func renderUserRef(ref openfga.RelationReference) string {
	switch {
	case ref.Type == "":
		return ""
	case ref.Wildcard != nil:
		return ref.Type + ":*"
	case ref.Relation != "":
		return ref.Type + "#" + ref.Relation
	default:
		return ref.Type
	}
}

// Empty reports whether the index carries no types at all.
func (ix *ModelIndex) Empty() bool { return ix == nil || len(ix.Types) == 0 }

// TypeNames returns every type, sorted.
func (ix *ModelIndex) TypeNames() []string {
	if ix == nil {
		return nil
	}
	names := make([]string, 0, len(ix.Types))
	for t := range ix.Types {
		names = append(names, t)
	}
	sort.Strings(names)
	return names
}

// RelationsFor returns typ's relations, sorted.
func (ix *ModelIndex) RelationsFor(typ string) []string {
	if ix == nil {
		return nil
	}
	return ix.Types[typ]
}

// UserTypesFor returns the user strings typ#relation directly accepts.
func (ix *ModelIndex) UserTypesFor(typ, relation string) []string {
	if ix == nil {
		return nil
	}
	return ix.UserTypes[typ+"#"+relation]
}

// ConditionNames returns every condition in the model, sorted.
func (ix *ModelIndex) ConditionNames() []string {
	if ix == nil {
		return nil
	}
	names := make([]string, 0, len(ix.Conditions))
	for name := range ix.Conditions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ConditionParams returns a condition's parameter names, sorted.
func (ix *ModelIndex) ConditionParams(name string) []string {
	if ix == nil {
		return nil
	}
	return ix.Conditions[name]
}
