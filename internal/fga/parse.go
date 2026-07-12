// Package fga holds OpenFGA domain helpers shared by the CLI commands and the
// TUI: parsing tuple shorthand and turning an authorization model into a graph.
package fga

import (
	"fmt"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
)

// ParseTuple parses the canonical "user relation object" triple from three
// separate arguments and returns a TupleKey. Each part is validated lightly:
// user and object should look like "type:id" (user may also carry "#relation"
// or be a wildcard "type:*").
func ParseTuple(user, relation, object string) (openfga.TupleKey, error) {
	user = strings.TrimSpace(user)
	relation = strings.TrimSpace(relation)
	object = strings.TrimSpace(object)

	if user == "" || relation == "" || object == "" {
		return openfga.TupleKey{}, fmt.Errorf("tuple requires user, relation and object (got user=%q relation=%q object=%q)", user, relation, object)
	}
	// user is "type:id", a wildcard "type:*", or a userset "type:id#relation".
	if !strings.Contains(user, ":") {
		return openfga.TupleKey{}, fmt.Errorf("user %q must be in the form type:id (e.g. user:anne, or a userset like team:eng#member) — did you swap the arguments? order is <user> <relation> <object>", user)
	}
	// object is a concrete "type:id" (no wildcard, no userset).
	typ, id := SplitObject(object)
	if typ == "" || id == "" {
		return openfga.TupleKey{}, fmt.Errorf("object %q must be in the form type:id (e.g. document:roadmap)", object)
	}
	if id == "*" || strings.Contains(object, "#") {
		return openfga.TupleKey{}, fmt.Errorf("object %q must be a concrete type:id, not a wildcard or userset", object)
	}
	return openfga.TupleKey{User: user, Relation: relation, Object: object}, nil
}

// FormatTuple renders a tuple key as "user relation object" with optional
// condition suffix.
func FormatTuple(k openfga.TupleKey) string {
	s := fmt.Sprintf("%s %s %s", k.User, k.Relation, k.Object)
	if k.Condition != nil && k.Condition.Name != "" {
		s += " [" + k.Condition.Name + "]"
	}
	return s
}

// SplitObject splits "type:id" into its type and id components.
func SplitObject(object string) (typ, id string) {
	if i := strings.Index(object, ":"); i >= 0 {
		return object[:i], object[i+1:]
	}
	return object, ""
}
