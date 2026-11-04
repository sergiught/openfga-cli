// Package bench synthesizes tuples and checks from an authorization model and
// benchmarks the checks against a before and an after model on a counting
// embedded OpenFGA server.
package bench

import openfgav1 "github.com/openfga/api/proto/openfga/v1"

// Check is a single benchmark query.
type Check struct {
	Name     string `json:"name" jsonschema_description:"Human-readable check label."`
	User     string `json:"user" jsonschema_description:"The user in the check, as type:id."`
	Relation string `json:"relation" jsonschema_description:"The relation checked."`
	Object   string `json:"object" jsonschema_description:"The object checked, as type:id."`
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
	User     string `json:"user" jsonschema_description:"The tuple user, as type:id or type:id#relation."`
	Relation string `json:"relation" jsonschema_description:"The tuple relation."`
	Object   string `json:"object" jsonschema_description:"The tuple object, as type:id."`
}

// Metrics are the measurements for one check against one model.
type Metrics struct {
	Reads      int64   `json:"reads" jsonschema_description:"Datastore reads the check performed."`
	Dispatches float64 `json:"dispatches" jsonschema_description:"Dispatches the check performed, averaged over the iterations."`
	P50Ms      float64 `json:"p50Ms" jsonschema_description:"Median latency in milliseconds."`
	P95Ms      float64 `json:"p95Ms" jsonschema_description:"95th percentile latency in milliseconds."`
}

// CheckResult is one check's outcome. After is nil for a single-model profile.
type CheckResult struct {
	Check
	Allowed bool     `json:"allowed" jsonschema_description:"The check answer under the before model."`
	Before  Metrics  `json:"before" jsonschema_description:"Metrics against the before model."`
	After   *Metrics `json:"after,omitempty" jsonschema_description:"Metrics against the after model; absent for a single-model profile."`
}

// Mismatch is a check whose answer differs between the two models.
type Mismatch struct {
	Check
	AllowedBefore bool `json:"allowedBefore" jsonschema_description:"The answer under the before model."`
	AllowedAfter  bool `json:"allowedAfter" jsonschema_description:"The answer under the after model."`
}

// Totals sums metrics by the before answer: deny checks (false) and allow
// checks (true).
type Totals struct {
	DenyReadsBefore      int64   `json:"denyReadsBefore" jsonschema_description:"Datastore reads summed over checks denied before, against the before model."`
	DenyReadsAfter       int64   `json:"denyReadsAfter" jsonschema_description:"Datastore reads summed over checks denied before, against the after model."`
	DenyDispatchesBefore float64 `json:"denyDispatchesBefore" jsonschema_description:"Dispatches summed over checks denied before, against the before model."`
	DenyDispatchesAfter  float64 `json:"denyDispatchesAfter" jsonschema_description:"Dispatches summed over checks denied before, against the after model."`
	AllowReadsBefore     int64   `json:"allowReadsBefore" jsonschema_description:"Datastore reads summed over checks allowed before, against the before model."`
	AllowReadsAfter      int64   `json:"allowReadsAfter" jsonschema_description:"Datastore reads summed over checks allowed before, against the after model."`
}

// Result is the benchmark outcome.
type Result struct {
	Source     string        `json:"source" jsonschema_description:"Where the tuples and checks came from: workspace or synthesis."`
	Seed       uint64        `json:"seed,omitempty" jsonschema_description:"The synthesis seed, for reproducing the run."`
	TupleCount int           `json:"tupleCount" jsonschema_description:"Number of tuples the benchmark loaded."`
	Iterations int           `json:"iterations" jsonschema_description:"Timed iterations per check."`
	Tuples     []Tuple       `json:"tuples,omitempty" jsonschema_description:"The synthesized tuple set, for reproducing the run; absent for workspace sources."` // synthesis only
	Skipped    []string      `json:"skipped,omitempty" jsonschema_description:"Restrictions or checks synthesis could not cover."`
	Checks     []CheckResult `json:"checks" jsonschema_description:"Per-check results."`
	Totals     Totals        `json:"totals" jsonschema_description:"Metrics summed by the before answer."`
	Probes     int           `json:"probes" jsonschema_description:"Number of untimed answer-only probe checks compared between the models."`
	Mismatches []Mismatch    `json:"mismatches" jsonschema_description:"Checks whose answer differs between the models and is attributable to the rewrite."`
	// Unstable are differing answers that are engine nondeterminism, not an
	// effect of the rewrite: the original model's answer varied between
	// re-runs, or the check cannot reach any relation the rewrite changed.
	Unstable []Mismatch `json:"unstable" jsonschema_description:"Differing answers that are engine nondeterminism, not an effect of the rewrite."`
	// Unexercised are rewritten relations ("type#relation") whose dependency
	// path reaches a restriction synthesis skipped, so the benchmark never
	// exercised the rewrite. Set by the command for synthesized sources only.
	Unexercised []string `json:"unexercised,omitempty" jsonschema_description:"Relations the rewrite changed that no benchmark check exercised."`
}
