package optimize

import "strconv"

// Tier classifies how a finding may be acted on.
type Tier string

const (
	// TierSafe findings are equivalence-preserving rewrites.
	TierSafe Tier = "safe"
	// TierCaller findings require a change in how callers use the model.
	TierCaller Tier = "caller"
	// TierTuple findings require a change in stored tuples.
	TierTuple Tier = "tuple"
)

// Finding is a single optimizer observation or rewrite.
type Finding struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Relation string `json:"relation"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Reason   string `json:"reason"`
	Proof    string `json:"proof"`
	Tier     Tier   `json:"tier"`
	// Key is the serialized FindingKey, set by Analyze so JSON consumers can
	// correlate findings.
	Key string `json:"key" jsonschema_description:"Stable identity: type#relation:id:proof, with #N appended when the same finding is applied more than once."`
	// Weights maps user type to weight; Analyze substitutes -1 for
	// graph.Infinite here and the command renders -1 as "inf".
	Weights map[string]int `json:"weights" jsonschema_description:"Weight per user type; -1 means unbounded (infinite) weight."`

	// apply performs the edit on a model structurally identical to the one
	// the finding was derived from. Never serialized.
	apply func(*Model)
}

// GatedFinding is a safe rewrite that was derived but not applied because a
// safety gate rejected it.
type GatedFinding struct {
	Finding
	Reason string `json:"reason" jsonschema_description:"Why the rewrite was not applied."`
}

// FindingKey identifies one applied finding by content. N only separates
// findings that are identical in every other field: it counts the earlier
// ones in the run.
type FindingKey struct {
	Type, Relation, ID, Proof string
	N                         int
}

// String renders the key as Type#Relation:ID:Proof, with #N appended when N > 0.
func (k FindingKey) String() string {
	s := k.Type + "#" + k.Relation + ":" + k.ID + ":" + k.Proof
	if k.N > 0 {
		s += "#" + strconv.Itoa(k.N)
	}
	return s
}

// Keys returns the key of each applied finding, in order.
func Keys(applied []Finding) []FindingKey {
	keys := make([]FindingKey, 0, len(applied))
	count := map[FindingKey]int{}
	for _, f := range applied {
		k := FindingKey{Type: f.Type, Relation: f.Relation, ID: f.ID, Proof: f.Proof}
		n := count[k]
		count[k]++
		k.N = n
		keys = append(keys, k)
	}
	return keys
}
