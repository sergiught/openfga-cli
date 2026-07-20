package modeltest

// resultsSchemaVersion is stamped onto every Results as SchemaVersion so tools
// consuming the JSON/YAML report (dashboards, CI) can depend on a stable shape
// and detect breaking changes. Bump it when the serialized shape changes
// incompatibly.
const resultsSchemaVersion = 1

// Results is the outcome of running a set of tests.
type Results struct {
	SchemaVersion int          `json:"schema_version"`
	Summary       Summary      `json:"summary"`
	Tests         []TestResult `json:"tests"`
	Coverage      *Coverage    `json:"coverage,omitempty"`
	// CoverageError carries the reason coverage could not be aggregated when
	// Options.Coverage was set (e.g. a multi-model workspace has no single
	// model to enumerate against). It is set only when Coverage is nil; the
	// tests themselves still ran. Empty on the successful (Coverage populated)
	// path.
	CoverageError string `json:"coverage_error,omitempty"`
}

// Summary aggregates counts across all matched tests.
type Summary struct {
	Total        int `json:"total"`
	Passed       int `json:"passed"`
	Failed       int `json:"failed"`
	MatchedFiles int `json:"matched_files"`
	MatchedTests int `json:"matched_tests"`
	// Incomplete reports that --fail-fast stopped the run before every matched
	// test ran, so counts (and any coverage) reflect only the tests that ran.
	Incomplete bool  `json:"incomplete,omitempty"`
	DurationMs int64 `json:"duration_ms"` // total wall-clock of the run (tests run in parallel, so this is NOT the sum of TestResult.DurationMs)
}

// TestResult is the outcome of a single test.
type TestResult struct {
	Name        string            `json:"name"`                  // "<file-stem>/<test-name>"
	Description string            `json:"description,omitempty"` // the test's optional `description:`, surfaced in the failure header
	Passed      bool              `json:"passed"`
	Assertions  []AssertionResult `json:"assertions"`
	DurationMs  int64             `json:"duration_ms"`
	// Error is set when the test could not be executed at all (model/fixture
	// load, store setup, or an engine error mid-assertion) rather than an
	// assertion simply failing. Such a test is reported as failed and errored,
	// but — like go test / pytest — it is isolated to itself so the rest of the
	// suite still runs and reports. Empty on a test that executed to completion.
	Error string `json:"error,omitempty"`
}

// Assertion kinds — the value of AssertionResult.Kind (and the corresponding
// test-file keyword). Constants so the producer (assert.go) and the consumers
// (explain/report) can't drift apart on a bare string.
const (
	kindCheck       = "check"
	kindListObjects = "list_objects"
	kindListUsers   = "list_users"
)

// AssertionResult is the outcome of a single check/list_objects/list_users
// assertion within a test.
type AssertionResult struct {
	Kind     string   `json:"kind"` // one of kindCheck / kindListObjects / kindListUsers
	Subject  string   `json:"subject"`
	Expected any      `json:"expected"`
	Got      any      `json:"got"`
	Passed   bool     `json:"passed"`
	Explain  *Explain `json:"explain,omitempty"`

	// covKey is the "type#relation" this assertion exercised, set for
	// list_objects/list_users so coverage can credit it structurally (see
	// markStructuralCredit) instead of reverse-parsing the display Subject.
	// Unexported: internal to the runner and never serialized.
	covKey string
}

// Coverage summarizes how many rewrite branches (see branchDef) were exercised
// by a test run. Aggregation from traversed branches into this shape happens
// in coverage.go; this file only defines the result types.
type Coverage struct {
	Total       int           `json:"total"`
	Covered     int           `json:"covered"`
	Percent     float64       `json:"percent"`
	Types       []TypeCov     `json:"types"`
	Unreachable []string      `json:"unreachable,omitempty"`
	Diff        *CoverageDiff `json:"diff,omitempty"`
	// Bounded reports that at least one assertion's resolution tree was truncated
	// by the trace budget (deep/wide model), so this coverage may be
	// under-reported. Callers gating on it (--coverage-min / --coverage-diff)
	// should surface a warning rather than silently failing on partial data.
	Bounded bool `json:"bounded,omitempty"`
}

// CoverageDiff reports the rewrite branches a change added versus a base model
// (e.g. the model on another git ref) and whether each is covered by a test —
// so a PR that adds an untested relation, arm, or condition can be caught.
type CoverageDiff struct {
	Base      string       `json:"base"`      // what the model was compared against (e.g. a git ref)
	Added     []DiffBranch `json:"added"`     // branches present now but not in the base model
	Uncovered int          `json:"uncovered"` // how many Added branches are not covered
}

// DiffBranch is a single branch that a change added, with its coverage status.
type DiffBranch struct {
	Type     string `json:"type"`
	Relation string `json:"relation"`
	Label    string `json:"label"`
	Covered  bool   `json:"covered"`
}

// TypeCov is the per-object-type rollup of Coverage.
type TypeCov struct {
	Type      string   `json:"type"`
	Covered   int      `json:"covered"`
	Total     int      `json:"total"`
	Relations []RelCov `json:"relations"`
}

// RelCov is the per-relation rollup of Coverage.
type RelCov struct {
	Relation string      `json:"relation"`
	Covered  int         `json:"covered"`
	Total    int         `json:"total"`
	Missed   []string    `json:"missed,omitempty"`   // branchDef.Label values not covered
	Branches []BranchCov `json:"branches,omitempty"` // full per-branch detail
}

// BranchCov is the per-branch detail underlying a RelCov: every branch the
// relation's rewrite enumerates, covered or not.
type BranchCov struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Covered bool   `json:"covered"`
}

// Explain is the resolution narrative for a single assertion: the engine's
// verdict plus a faithful tree of how that verdict was reached.
type Explain struct {
	Verdict     bool         `json:"verdict"`
	Tree        *ExplainNode `json:"tree,omitempty"`
	NearestMiss string       `json:"nearest_miss,omitempty"` // set on a failed check assertion, see explainCheck
	SetDiff     *SetDiff     `json:"set_diff,omitempty"`     // set on list_objects/list_users assertions, see setDiff
	// Bounded reports that the resolution tree was truncated by the trace
	// depth/node budget: some arms were never expanded, so coverage computed from
	// this tree is partial (under-credited), not wrong. See fga.ExpandTree.
	Bounded bool `json:"bounded,omitempty"`

	// grantedArms and subtractRels carry grant-based coverage credit derived from
	// the marked resolution tree: grantedArms maps a "type#relation" to the arm
	// branch labels (direct:.../wildcard:.../computed:.../ttu:...) that were shown
	// to grant, and subtractRels holds the relations whose difference (but-not)
	// arm was actually exercised. Both are internal to coverage aggregation and
	// never serialized. See computeArmGrants and branchCovered.
	grantedArms  map[string]map[string]bool
	subtractRels map[string]bool
}

// ExplainNode is one node in the resolution tree: a relation being resolved
// or a leaf, with whether it granted and a short reason.
type ExplainNode struct {
	Label    string         `json:"label"`
	Rel      string         `json:"rel,omitempty"` // this node's own "type#relation" key, e.g. "document#viewer"; empty for operator/union nodes
	Result   bool           `json:"result"`
	Reason   string         `json:"reason,omitempty"`
	Children []*ExplainNode `json:"children,omitempty"`

	// DirectMember reports that the queried user appears as a direct member of
	// this node's own direct-user leaf (a literal tuple, or a public-wildcard
	// tuple for the user's type). It is the signal coverage uses to tell a
	// condition denial (Result==false while the user IS directly assigned, so
	// only a failed ABAC condition can have denied) apart from a plain no-tuple
	// denial (user not assigned at all) — only the former exercises a
	// condition:<c>=false branch.
	DirectMember bool `json:"direct_member,omitempty"`

	// Conditions names the ABAC condition(s) carried by the direct tuple(s) that
	// make the queried user a direct member of this leaf (empty for an
	// unconditioned assignment). It is set only on a DirectMember leaf and lets
	// coverage credit the specific condition:<name>=true/=false branch that was
	// exercised, rather than every condition declared on the relation.
	Conditions []string `json:"conditions,omitempty"`
}

// SetDiff captures the difference between an expected and observed set of
// objects/users for list_objects/list_users assertions.
type SetDiff struct {
	Unexpected []string `json:"unexpected,omitempty"`
	Missing    []string `json:"missing,omitempty"`
}
