package mapping

import "github.com/openfga/mapper/language"

// Document is a mapping file under construction. It mirrors the language's
// shape rather than mapper's parsed form because the wizard edits partial,
// not-yet-valid states — a rule with no tuples yet, a tuple with an empty
// object — that mapper's own types cannot represent.
type Document struct {
	Rules []Rule
	Tests []TestCase
}

// Rule is one entry under `rules:`. Action is the rule-level action ("write" or
// "delete", or empty to leave it per-tuple); the language forbids setting it
// alongside any tuple-level action.
type Rule struct {
	Name   string
	When   string
	Action string

	Variables []Variable
	Iterator  *Iterator
	Filters   []TupleFilter
	Tuples    []Tuple

	// Sample is the event this rule is previewed against. It is wizard state,
	// not part of the file: BuildTests turns it into a `tests:` entry at save
	// time, and Marshal never writes it.
	Sample *Sample

	// AutoName and AutoWhen record what picking an event last auto-filled, so a
	// later event pick can replace an untouched value but never a hand-edited
	// one. Wizard state; never marshalled.
	AutoName string
	AutoWhen string
}

// Variable is one entry under `variables:`. Order matters: each expression sees
// input plus the variables declared before it, and forward references are a
// compile error.
type Variable struct {
	Name string
	Expr string
}

// Iterator fans a rule out over an array in the event. Source is an expression
// yielding the array; As names the element inside the iterator's tuples.
type Iterator struct {
	Source string
	As     string
	Tuples []Tuple
}

// TupleFilter is one entry under `tuple_filters:`, describing the slice of
// existing tuples a rule owns. Action is "patch" (default) or "delete".
type TupleFilter struct {
	User     string
	Relation string
	Object   string
	Action   string
}

// Tuple is one entry under `tuples:`. User, Relation and Object are templates
// with `{{ expr }}` interpolation; When gates this tuple alone; Action is
// "write" (default) or "delete"; Condition names an FGA condition and Context
// supplies its parameters.
type Tuple struct {
	User      string
	Relation  string
	Object    string
	When      string
	Action    string
	Condition string
	Context   []ContextEntry
}

// ContextEntry is one condition-context parameter. A slice, not a map, so the
// emitted order is the order the user entered.
type ContextEntry struct {
	Key      string
	Template string
}

// Sample is the event a rule is previewed against: an Auth0 catalog entry, a
// pasted payload, or one loaded from a file. Label is for display only.
type Sample struct {
	Label string
	Event map[string]any
}

// TestCase is one entry under `tests:`, generated from a rule's sample at save
// time so the file carries a regression test for what the wizard showed.
type TestCase struct {
	Name               string
	Input              map[string]any
	ExpectTuples       []language.Tuple
	ExpectTupleFilters []language.TupleFilter
}
