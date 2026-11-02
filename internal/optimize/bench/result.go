// Package bench synthesizes tuples and checks from an authorization model and
// benchmarks the checks against a before and an after model on a counting
// embedded OpenFGA server.
package bench

import openfgav1 "github.com/openfga/api/proto/openfga/v1"

// Check is a single benchmark query.
type Check struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// Source is the tuple set and checks a benchmark runs over.
type Source struct {
	Kind   string                `json:"kind"` // "workspace" | "synthesis"
	Seed   uint64                `json:"seed,omitempty"`
	Tuples []*openfgav1.TupleKey `json:"-"`
	Checks []Check               `json:"checks"`
	// Probes are untimed answer-only checks compared between the models.
	Probes  []Check  `json:"-"`
	Skipped []string `json:"skipped,omitempty"`
}

// Tuple is a relationship tuple without proto types, for serialized output.
type Tuple struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// Metrics are the measurements for one check against one model.
type Metrics struct {
	Reads      int64   `json:"reads"`
	Dispatches float64 `json:"dispatches"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
}

// CheckResult is one check's outcome. After is nil for a single-model profile.
type CheckResult struct {
	Check
	Allowed bool     `json:"allowed"`
	Before  Metrics  `json:"before"`
	After   *Metrics `json:"after,omitempty"`
}

// Mismatch is a check whose answer differs between the two models.
type Mismatch struct {
	Check
	AllowedBefore bool `json:"allowedBefore"`
	AllowedAfter  bool `json:"allowedAfter"`
}

// Totals sums metrics by the before answer: deny checks (false) and allow
// checks (true).
type Totals struct {
	DenyReadsBefore      int64   `json:"denyReadsBefore"`
	DenyReadsAfter       int64   `json:"denyReadsAfter"`
	DenyDispatchesBefore float64 `json:"denyDispatchesBefore"`
	DenyDispatchesAfter  float64 `json:"denyDispatchesAfter"`
	AllowReadsBefore     int64   `json:"allowReadsBefore"`
	AllowReadsAfter      int64   `json:"allowReadsAfter"`
}

// Result is the benchmark outcome.
type Result struct {
	Source     string        `json:"source"`
	Seed       uint64        `json:"seed,omitempty"`
	TupleCount int           `json:"tupleCount"`
	Iterations int           `json:"iterations"`
	Tuples     []Tuple       `json:"tuples,omitempty"` // synthesis only
	Skipped    []string      `json:"skipped,omitempty"`
	Checks     []CheckResult `json:"checks"`
	Totals     Totals        `json:"totals"`
	Probes     int           `json:"probes"`
	Mismatches []Mismatch    `json:"mismatches"`
	// Unstable are differing answers that are engine nondeterminism, not an
	// effect of the rewrite: the original model's answer varied between
	// re-runs, or the check cannot reach any relation the rewrite changed.
	Unstable []Mismatch `json:"unstable"`
}
