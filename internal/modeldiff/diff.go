// Package modeldiff compares two authorization models and classifies the
// differences between their shapes.
package modeldiff

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sergiught/openfga-cli/internal/optimize"
)

// Kind values are stable and part of the JSON contract.
const (
	// Breaking.
	TypeRemoved               = "type_removed"
	RelationRemoved           = "relation_removed"
	DirectTypeRemoved         = "direct_type_removed"
	ConditionRemoved          = "condition_removed"
	ConditionParamRemoved     = "condition_param_removed"
	ConditionParamTypeChanged = "condition_param_type_changed"
	// ConditionParamAdded is breaking: existing callers and stored tuple
	// contexts do not supply the new parameter, so checks can fail.
	ConditionParamAdded = "condition_param_added"

	// Changed.
	RewriteChanged             = "rewrite_changed"
	ConditionExpressionChanged = "condition_expression_changed"
	SchemaVersionChanged       = "schema_version_changed"

	// Added.
	TypeAdded       = "type_added"
	RelationAdded   = "relation_added"
	DirectTypeAdded = "direct_type_added"
	ConditionAdded  = "condition_added"
)

// Entry is one difference between the two models.
type Entry struct {
	Kind      string `json:"kind" jsonschema:"enum=type_removed,enum=relation_removed,enum=direct_type_removed,enum=condition_removed,enum=condition_param_removed,enum=condition_param_type_changed,enum=rewrite_changed,enum=condition_expression_changed,enum=schema_version_changed,enum=type_added,enum=relation_added,enum=direct_type_added,enum=condition_added,enum=condition_param_added"`
	Type      string `json:"type,omitempty"`
	Relation  string `json:"relation,omitempty"`
	Condition string `json:"condition,omitempty"`
	Detail    string `json:"detail"`
}

// Result groups the differences by severity.
type Result struct {
	Breaking []Entry `json:"breaking"`
	Changed  []Entry `json:"changed"`
	Added    []Entry `json:"added"`
	// Cost lists relations whose per-user-type weights differ.
	Cost []CostEntry `json:"cost"`
	// CostSkipped explains why Cost could not be computed.
	CostSkipped string `json:"costSkipped,omitempty"`
}

// Empty reports whether the models have no differences.
func (r Result) Empty() bool {
	return len(r.Breaking) == 0 && len(r.Changed) == 0 && len(r.Added) == 0 && len(r.Cost) == 0
}

// Diff compares the shapes of old and new.
func Diff(oldM, newM *optimize.Model) Result {
	r := Result{Breaking: []Entry{}, Changed: []Entry{}, Added: []Entry{}, Cost: []CostEntry{}}

	if ov, nv := oldM.Proto.GetSchemaVersion(), newM.Proto.GetSchemaVersion(); ov != nv {
		r.Changed = append(r.Changed, Entry{
			Kind:   SchemaVersionChanged,
			Detail: fmt.Sprintf("schema version %s → %s", ov, nv),
		})
	}

	oldTypes, newTypes := typeDefs(oldM), typeDefs(newM)
	for _, name := range sortedKeys(oldTypes) {
		if _, ok := newTypes[name]; !ok {
			r.Breaking = append(r.Breaking, Entry{Kind: TypeRemoved, Type: name, Detail: fmt.Sprintf("type `%s` removed", name)})
			continue
		}
		diffRelations(&r, oldM, newM, name, oldTypes[name], newTypes[name])
	}
	for _, name := range sortedKeys(newTypes) {
		if _, ok := oldTypes[name]; !ok {
			r.Added = append(r.Added, Entry{Kind: TypeAdded, Type: name, Detail: fmt.Sprintf("type `%s` added", name)})
		}
	}

	diffConditions(&r, oldM.Proto.GetConditions(), newM.Proto.GetConditions())

	sortEntries(r.Breaking)
	sortEntries(r.Changed)
	sortEntries(r.Added)
	diffCost(&r, oldM, newM)
	return r
}

func typeDefs(m *optimize.Model) map[string]*openfgav1.TypeDefinition {
	out := map[string]*openfgav1.TypeDefinition{}
	for _, td := range m.Proto.GetTypeDefinitions() {
		out[td.GetType()] = td
	}
	return out
}

func diffRelations(r *Result, oldM, newM *optimize.Model, typ string, oldTD, newTD *openfgav1.TypeDefinition) {
	for _, rel := range sortedKeys(oldTD.GetRelations()) {
		oldUS, _ := oldM.Relation(typ, rel)
		newUS, ok := newM.Relation(typ, rel)
		if !ok {
			r.Breaking = append(r.Breaking, Entry{
				Kind: RelationRemoved, Type: typ, Relation: rel,
				Detail: fmt.Sprintf("relation `%s#%s` removed", typ, rel),
			})
			continue
		}

		oldKeys := directKeys(oldM.DirectTypes(typ, rel))
		newKeys := directKeys(newM.DirectTypes(typ, rel))
		for _, k := range sortedKeys(oldKeys) {
			if !newKeys[k] {
				r.Breaking = append(r.Breaking, Entry{
					Kind: DirectTypeRemoved, Type: typ, Relation: rel,
					Detail: fmt.Sprintf("`%s` no longer assignable to `%s#%s`", k, typ, rel),
				})
			}
		}
		for _, k := range sortedKeys(newKeys) {
			if !oldKeys[k] {
				r.Added = append(r.Added, Entry{
					Kind: DirectTypeAdded, Type: typ, Relation: rel,
					Detail: fmt.Sprintf("`%s` now assignable to `%s#%s`", k, typ, rel),
				})
			}
		}

		if !bytes.Equal(canonical(oldUS), canonical(newUS)) {
			r.Changed = append(r.Changed, Entry{
				Kind: RewriteChanged, Type: typ, Relation: rel,
				Detail: fmt.Sprintf("rewrite of `%s#%s` changed; access may differ (access analysis not done yet)", typ, rel),
			})
		}
	}
	for _, rel := range sortedKeys(newTD.GetRelations()) {
		if _, ok := oldTD.GetRelations()[rel]; !ok {
			r.Added = append(r.Added, Entry{
				Kind: RelationAdded, Type: typ, Relation: rel,
				Detail: fmt.Sprintf("relation `%s#%s` added", typ, rel),
			})
		}
	}
}

// directKeys returns the DSL spelling of each restriction element.
func directKeys(refs []*openfgav1.RelationReference) map[string]bool {
	out := map[string]bool{}
	for _, ref := range refs {
		k := ref.GetType()
		switch {
		case ref.GetWildcard() != nil:
			k += ":*"
		case ref.GetRelation() != "":
			k += "#" + ref.GetRelation()
		}
		if c := ref.GetCondition(); c != "" {
			k += " with " + c
		}
		out[k] = true
	}
	return out
}

// canonical serializes us deterministically, ignoring the order of union and
// intersection children.
func canonical(us *openfgav1.Userset) []byte {
	return marshal(canonicalize(us))
}

func marshal(m proto.Message) []byte {
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	return b
}

func canonicalize(us *openfgav1.Userset) *openfgav1.Userset {
	if us == nil {
		return nil
	}
	out := proto.Clone(us).(*openfgav1.Userset)
	switch v := out.GetUserset().(type) {
	case *openfgav1.Userset_Union:
		v.Union.Child = sortedChildren(v.Union.GetChild())
	case *openfgav1.Userset_Intersection:
		v.Intersection.Child = sortedChildren(v.Intersection.GetChild())
	case *openfgav1.Userset_Difference:
		v.Difference.Base = canonicalize(v.Difference.GetBase())
		v.Difference.Subtract = canonicalize(v.Difference.GetSubtract())
	}
	return out
}

func sortedChildren(children []*openfgav1.Userset) []*openfgav1.Userset {
	type keyed struct {
		us  *openfgav1.Userset
		key []byte
	}
	ks := make([]keyed, len(children))
	for i, c := range children {
		cc := canonicalize(c)
		ks[i] = keyed{cc, marshal(cc)}
	}
	sort.Slice(ks, func(i, j int) bool { return bytes.Compare(ks[i].key, ks[j].key) < 0 })
	out := make([]*openfgav1.Userset, len(ks))
	for i, k := range ks {
		out[i] = k.us
	}
	return out
}

func diffConditions(r *Result, oldC, newC map[string]*openfgav1.Condition) {
	for _, name := range sortedKeys(oldC) {
		oc := oldC[name]
		nc, ok := newC[name]
		if !ok {
			r.Breaking = append(r.Breaking, Entry{Kind: ConditionRemoved, Condition: name, Detail: fmt.Sprintf("condition `%s` removed", name)})
			continue
		}
		for _, p := range sortedKeys(oc.GetParameters()) {
			np, ok := nc.GetParameters()[p]
			if !ok {
				r.Breaking = append(r.Breaking, Entry{
					Kind: ConditionParamRemoved, Condition: name,
					Detail: fmt.Sprintf("parameter `%s` removed from condition `%s`", p, name),
				})
				continue
			}
			if ot, nt := paramType(oc.GetParameters()[p]), paramType(np); ot != nt {
				r.Breaking = append(r.Breaking, Entry{
					Kind: ConditionParamTypeChanged, Condition: name,
					Detail: fmt.Sprintf("parameter `%s` of condition `%s` changed type %s → %s", p, name, ot, nt),
				})
			}
		}
		for _, p := range sortedKeys(nc.GetParameters()) {
			if _, ok := oc.GetParameters()[p]; !ok {
				r.Breaking = append(r.Breaking, Entry{
					Kind: ConditionParamAdded, Condition: name,
					Detail: fmt.Sprintf("parameter `%s` added to condition `%s`", p, name),
				})
			}
		}
		if canonicalCEL(oc.GetExpression()) != canonicalCEL(nc.GetExpression()) {
			r.Changed = append(r.Changed, Entry{
				Kind: ConditionExpressionChanged, Condition: name,
				Detail: fmt.Sprintf("expression of condition `%s` changed", name),
			})
		}
	}
	for _, name := range sortedKeys(newC) {
		if _, ok := oldC[name]; !ok {
			r.Added = append(r.Added, Entry{Kind: ConditionAdded, Condition: name, Detail: fmt.Sprintf("condition `%s` added", name)})
		}
	}
}

// paramType renders a parameter type such as `map<string>`.
func paramType(ref *openfgav1.ConditionParamTypeRef) string {
	name := strings.ToLower(strings.TrimPrefix(ref.GetTypeName().String(), "TYPE_NAME_"))
	if len(ref.GetGenericTypes()) == 0 {
		return name
	}
	parts := make([]string, len(ref.GetGenericTypes()))
	for i, g := range ref.GetGenericTypes() {
		parts[i] = paramType(g)
	}
	return name + "<" + strings.Join(parts, ", ") + ">"
}

func sortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		switch {
		case a.Type != b.Type:
			return a.Type < b.Type
		case a.Relation != b.Relation:
			return a.Relation < b.Relation
		case a.Condition != b.Condition:
			return a.Condition < b.Condition
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// canonicalCEL unparses a CEL expression so formatting-only edits compare
// equal. It parses without type-checking; an expression that does not parse
// or unparse is compared as written.
func canonicalCEL(expr string) string {
	env, err := cel.NewEnv()
	if err != nil {
		return expr
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return expr
	}
	out, err := cel.AstToString(ast)
	if err != nil {
		return expr
	}
	return out
}
