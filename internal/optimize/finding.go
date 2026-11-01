package optimize

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
	// Weights maps user type to weight; Analyze substitutes -1 for
	// graph.Infinite here and the command renders -1 as "inf".
	Weights map[string]int `json:"weights"`
}
