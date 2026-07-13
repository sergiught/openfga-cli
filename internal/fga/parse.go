// Package fga holds OpenFGA domain helpers shared by the CLI commands and the
// TUI: parsing tuple shorthand and turning an authorization model into a graph.
package fga

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
)

// ParseJSONObject parses s as a JSON object into a map, returning nil for empty
// input. label names the field in the error message (e.g. "--context" or
// "context") so both the CLI and TUI can share one implementation.
func ParseJSONObject(label, s string) (map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", label, err)
	}
	return m, nil
}

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

// Triple resolves a user/relation/object triple from positional args and the
// --user/--relation/--object flags. Flags set the fields they name; the
// remaining positionals then fill the still-unset fields left to right. This
// means `--user user:anne viewer document:roadmap` reads the two positionals as
// relation and object (rather than shifting them by index). Extra positionals
// that can't fill an unset field are an error, so a flag and a positional never
// silently fight over the same field. It errors if any part is missing.
func Triple(args []string, userFlag, relationFlag, objectFlag string) (user, relation, object string, err error) {
	user, relation, object = userFlag, relationFlag, objectFlag
	rest := args
	fill := func(field *string) {
		if *field == "" && len(rest) > 0 {
			*field, rest = rest[0], rest[1:]
		}
	}
	fill(&user)
	fill(&relation)
	fill(&object)
	if len(rest) > 0 {
		return "", "", "", fmt.Errorf("too many arguments: %v — <user>/<relation>/<object> already set via flags and positionals", rest)
	}
	if user == "" || relation == "" || object == "" {
		return "", "", "", errors.New("provide <user> <relation> <object> (as arguments or via --user/--relation/--object)")
	}
	return user, relation, object, nil
}

// FormatTuple renders a tuple key as "user relation object" with an optional
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
	typ, id, _ = strings.Cut(object, ":")
	return typ, id
}
