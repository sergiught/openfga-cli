package playground

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/fga"
)

// This file holds the parsers that turn the free-text condition / contextual-
// tuple form fields into the typed values the OpenFGA requests expect. All
// inputs are optional: empty text parses to a nil value.

// parseContextJSON parses a JSON object of condition parameters (the request
// `context`). Empty input yields nil.
func parseContextJSON(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("context must be a JSON object: %w", err)
	}
	return m, nil
}

// parseContextualTuples parses `;`-separated `user relation object` tuples into
// contextual tuple keys. Empty input yields nil.
func parseContextualTuples(s string) ([]openfga.TupleKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []openfga.TupleKey
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f := strings.Fields(part)
		if len(f) != 3 {
			return nil, fmt.Errorf("contextual tuple %q must be `user relation object`", part)
		}
		k, err := fga.ParseTuple(f[0], f[1], f[2])
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// queryCtx bundles the optional condition context and contextual tuples a
// relationship query runs with.
type queryCtx struct {
	context    map[string]any
	contextual []openfga.TupleKey
}

// parseQueryCtx parses the query form's optional context (JSON) and contextual
// tuples fields into a queryCtx.
func parseQueryCtx(contextJSON, contextual string) (queryCtx, error) {
	cm, err := parseContextJSON(contextJSON)
	if err != nil {
		return queryCtx{}, err
	}
	ct, err := parseContextualTuples(contextual)
	if err != nil {
		return queryCtx{}, err
	}
	return queryCtx{context: cm, contextual: ct}, nil
}

// contextualTupleKeys wraps contextual tuples for a request, or nil when empty.
func contextualTupleKeys(tuples []openfga.TupleKey) *openfga.ContextualTupleKeys {
	if len(tuples) == 0 {
		return nil
	}
	return &openfga.ContextualTupleKeys{TupleKeys: tuples}
}

// formatContextualTuples renders contextual tuples back into the `;`-separated
// form the parser accepts, for editing.
func formatContextualTuples(tuples []openfga.TupleKey) string {
	if len(tuples) == 0 {
		return ""
	}
	parts := make([]string, len(tuples))
	for i, t := range tuples {
		parts[i] = t.User + " " + t.Relation + " " + t.Object
	}
	return strings.Join(parts, "; ")
}

// formatContextJSON renders a context map back to compact JSON for editing.
func formatContextJSON(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseCondition builds a tuple's ABAC condition from a name and optional JSON
// context. An empty name yields nil (an unconditioned tuple).
func parseCondition(name, contextJSON string) (*openfga.RelationshipCondition, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	ctx, err := parseContextJSON(contextJSON)
	if err != nil {
		return nil, err
	}
	return &openfga.RelationshipCondition{Name: name, Context: ctx}, nil
}
