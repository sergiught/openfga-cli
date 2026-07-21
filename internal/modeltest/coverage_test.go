package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadModelDSL writes dsl to a temp .fga file via the shared writeModel test
// helper (see explain_test.go) and loads it.
func loadModelDSL(t *testing.T, dsl string) *LoadedModel {
	t.Helper()

	lm, err := loadModel(writeModel(t, dsl))
	if err != nil {
		t.Fatalf("loadModel: %v", err)
	}
	return lm
}

func TestEnumerateBranchesCountsButNotArm(t *testing.T) {
	dsl := `model
  schema 1.1

type user

type document
  relations
    define banned: [user]
    define viewer: [user] but not banned
`
	lm := loadModelDSL(t, dsl)

	branches, unreachable := enumerateBranches(lm)

	var hasDirect, hasSubtract bool
	for _, b := range branches {
		if b.Type != "document" || b.Relation != "viewer" {
			continue
		}
		if b.Kind == "direct" && b.Label == "direct:user" {
			hasDirect = true
		}
		if b.Kind == "difference-subtract" && b.Label == "but-not:banned" {
			hasSubtract = true
		}
	}

	if !hasDirect {
		t.Errorf("expected a direct:user branch for document#viewer, got %+v", branches)
	}
	if !hasSubtract {
		t.Errorf("expected a but-not:banned difference-subtract branch for document#viewer, got %+v", branches)
	}
	if len(unreachable) != 0 {
		t.Errorf("expected no unreachable relations, got %v", unreachable)
	}
}

func TestEnumerateBranchesConditionDoublesBranch(t *testing.T) {
	dsl := `model
  schema 1.1

type user

type document
  relations
    define viewer: [user with non_expired_grant]

condition non_expired_grant(current_time: timestamp, grant_time: timestamp, grant_duration: duration) {
  current_time < grant_time + grant_duration
}
`
	lm := loadModelDSL(t, dsl)

	branches, _ := enumerateBranches(lm)

	var hasTrue, hasFalse bool
	for _, b := range branches {
		if b.Type != "document" || b.Relation != "viewer" {
			continue
		}
		if b.Kind == "condition" && b.Label == "condition:non_expired_grant=true" {
			hasTrue = true
		}
		if b.Kind == "condition" && b.Label == "condition:non_expired_grant=false" {
			hasFalse = true
		}
	}

	if !hasTrue {
		t.Errorf("expected a condition:non_expired_grant=true branch, got %+v", branches)
	}
	if !hasFalse {
		t.Errorf("expected a condition:non_expired_grant=false branch, got %+v", branches)
	}
}

// TestEnumerateBranchesConditionedUsersetNoConditionBranches pins the fix that a
// conditioned USERSET assignment ([group#member with cond]) does NOT emit
// condition:true/false branches: condition credit flows only through
// direct-member concrete/wildcard leaves (isDirectMember never matches a userset
// leaf), so such branches could never be covered — phantom uncovered noise. The
// direct:group#member branch itself must still be emitted.
func TestEnumerateBranchesConditionedUsersetNoConditionBranches(t *testing.T) {
	dsl := `model
  schema 1.1

type user

type group
  relations
    define member: [user]

type document
  relations
    define viewer: [group#member with cond]

condition cond(x: bool) {
  x == true
}
`
	lm := loadModelDSL(t, dsl)
	branches, _ := enumerateBranches(lm)

	var hasDirect bool
	for _, b := range branches {
		if b.Type != "document" || b.Relation != "viewer" {
			continue
		}
		if b.Kind == "direct" && b.Label == "direct:group#member" {
			hasDirect = true
		}
		if b.Kind == "condition" {
			t.Errorf("conditioned userset assignment must emit no condition branch, got %q", b.Label)
		}
	}
	if !hasDirect {
		t.Errorf("expected the direct:group#member branch to still be emitted, got %+v", branches)
	}
}

// TestCoverageConditionedUsersetNoPhantomUncovered exercises the same
// conditioned-userset relation with a passing check and asserts the run reports
// no missed condition branches (which, being uncreditable, would otherwise
// permanently depress coverage and trip --coverage-diff).
func TestCoverageConditionedUsersetNoPhantomUncovered(t *testing.T) {
	dir := t.TempDir()

	modelDSL := `model
  schema 1.1

type user

type group
  relations
    define member: [user]

type document
  relations
    define viewer: [group#member with cond]

condition cond(x: bool) {
  x == true
}
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	testDSL := `model: ./model.fga
tests:
  - name: userset-cond
    tuples:
      - user: user:anne
        relation: member
        object: group:eng
      - user: group:eng#member
        relation: viewer
        object: document:1
        condition: {name: cond}
    check:
      - user: user:anne
        object: document:1
        context: {x: true}
        assertions: {viewer: true}
`
	testPath := filepath.Join(dir, "cov.test.yaml")
	if err := os.WriteFile(testPath, []byte(testDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(testPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Failed != 0 {
		t.Fatalf("want 0 failed, got %d: %+v", res.Summary.Failed, res.Tests)
	}

	rc := findRelCov(res.Coverage, "document", "viewer")
	if rc == nil {
		t.Fatal("expected document#viewer in coverage")
	}
	for _, m := range rc.Missed {
		if strings.HasPrefix(m, "condition:") {
			t.Errorf("no phantom condition branch should appear in Missed, got %q", m)
		}
	}
	if rc.Covered != rc.Total {
		t.Errorf("document#viewer should be fully covered (no uncreditable branches), got %d/%d Missed=%v", rc.Covered, rc.Total, rc.Missed)
	}
}

// findRelCov returns the RelCov for (typ, relation) in cov, or nil.
func findRelCov(cov *Coverage, typ, relation string) *RelCov {
	for _, tc := range cov.Types {
		if tc.Type != typ {
			continue
		}
		for i := range tc.Relations {
			if tc.Relations[i].Relation == relation {
				return &tc.Relations[i]
			}
		}
	}
	return nil
}

// TestCoverageMarksResolvedRelationsCovered runs the docs workspace with
// branch coverage on and checks that document#viewer — resolved by both of
// the workspace's check assertions — comes back with covered branches,
// while folder#viewer — never resolved, since the docs fixtures never set a
// document's "parent" tuple, so the "viewer from parent" arm never expands
// into folder#viewer — comes back entirely uncovered.
func TestCoverageMarksResolvedRelationsCovered(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage == nil {
		t.Fatal("want non-nil Coverage")
	}
	if res.Coverage.Total == 0 {
		t.Fatal("want Coverage.Total > 0")
	}

	viewerRC := findRelCov(res.Coverage, "document", "viewer")
	if viewerRC == nil {
		t.Fatal("expected document#viewer in coverage")
	}
	if viewerRC.Covered == 0 {
		t.Errorf("expected document#viewer to have covered branches, got %+v", viewerRC)
	}

	folderViewerRC := findRelCov(res.Coverage, "folder", "viewer")
	if folderViewerRC == nil {
		t.Fatal("expected folder#viewer in coverage")
	}
	if folderViewerRC.Covered != 0 {
		t.Errorf("expected folder#viewer to be entirely uncovered, got %+v", folderViewerRC)
	}
	if len(folderViewerRC.Missed) == 0 {
		t.Errorf("expected folder#viewer to list missed branches, got %+v", folderViewerRC)
	}
}

// TestCoverageConditionSideBySide runs only the abac workspace's
// condition-satisfied test (via --run), so the condition is exercised on
// only one side, and checks that the two condition branches are reported
// asymmetrically: =true covered, =false missed.
func TestCoverageConditionSideBySide(t *testing.T) {
	ws, err := LoadWorkspace("testdata/abac")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true, Run: "abac/condition-satisfied"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage == nil {
		t.Fatal("want non-nil Coverage")
	}

	rc := findRelCov(res.Coverage, "document", "viewer")
	if rc == nil {
		t.Fatal("expected document#viewer in coverage")
	}

	for _, m := range rc.Missed {
		if m == "condition:in_business_hours=true" {
			t.Errorf("expected condition:in_business_hours=true to be covered, found it in Missed: %+v", rc.Missed)
		}
	}

	hasMissedFalse := false
	for _, m := range rc.Missed {
		if m == "condition:in_business_hours=false" {
			hasMissedFalse = true
		}
	}
	if !hasMissedFalse {
		t.Errorf("expected condition:in_business_hours=false to be missed (only the satisfied case ran), got %+v", rc.Missed)
	}
}

// missedContains reports whether label is in rc.Missed.
func missedContains(rc *RelCov, label string) bool {
	for _, m := range rc.Missed {
		if m == label {
			return true
		}
	}
	return false
}

// TestCoverageConditionFalseNotOverCredited pins the fix for the over-credited
// condition:<c>=false branch: a conditioned relation's =false branch must count
// covered only when a check genuinely exercised a failed condition (an assigned
// user whose condition evaluated false), NOT when some unrelated user was simply
// denied for having no tuple at all.
//
// The workspace assigns user:anne to doc:1#viewer under the valid_grant
// condition and holds two isolated tests so a single run exercises exactly one
// denial shape:
//   - no-tuple-deny: anne (condition true) => allow, plus user:bob (never
//     assigned) => deny. The deny is a plain no-tuple denial, so =false must
//     stay MISSED while =true is covered.
//   - condition-deny: anne (condition true) => allow, plus anne (condition
//     false via context) => deny. That deny IS condition-driven, so =false must
//     be covered.
func TestCoverageConditionFalseNotOverCredited(t *testing.T) {
	dir := t.TempDir()

	modelDSL := `model
  schema 1.1

type user

type doc
  relations
    define viewer: [user with valid_grant]

condition valid_grant(authorized: bool) {
  authorized == true
}
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	testDSL := `model: ./model.fga
tests:
  - name: no-tuple-deny
    tuples:
      - user: user:anne
        relation: viewer
        object: doc:1
        condition: {name: valid_grant}
    check:
      - user: user:anne
        object: doc:1
        context: {authorized: true}
        assertions: {viewer: true}
      - user: user:bob
        object: doc:1
        context: {authorized: true}
        assertions: {viewer: false}
  - name: condition-deny
    tuples:
      - user: user:anne
        relation: viewer
        object: doc:1
        condition: {name: valid_grant}
    check:
      - user: user:anne
        object: doc:1
        context: {authorized: true}
        assertions: {viewer: true}
      - user: user:anne
        object: doc:1
        context: {authorized: false}
        assertions: {viewer: false}
`
	testPath := filepath.Join(dir, "cov.test.yaml")
	if err := os.WriteFile(testPath, []byte(testDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(testPath)
	if err != nil {
		t.Fatal(err)
	}

	runCov := func(run string) *RelCov {
		eng, err := NewEmbeddedEngine(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
		res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true, Run: run})
		if err != nil {
			t.Fatalf("Run(%q): %v", run, err)
		}
		rc := findRelCov(res.Coverage, "doc", "viewer")
		if rc == nil {
			t.Fatalf("Run(%q): expected doc#viewer in coverage", run)
		}
		return rc
	}

	// No-tuple denial: =true covered, =false must NOT be over-credited.
	noTuple := runCov("no-tuple-deny")
	if missedContains(noTuple, "condition:valid_grant=true") {
		t.Errorf("no-tuple-deny: condition:valid_grant=true should be covered, got Missed=%v", noTuple.Missed)
	}
	if !missedContains(noTuple, "condition:valid_grant=false") {
		t.Errorf("no-tuple-deny: condition:valid_grant=false must stay MISSED (no failed condition was exercised), got Missed=%v", noTuple.Missed)
	}

	// Genuine condition-driven denial: =false must now be covered.
	condDeny := runCov("condition-deny")
	if missedContains(condDeny, "condition:valid_grant=false") {
		t.Errorf("condition-deny: condition:valid_grant=false should be covered by a failed-condition check, got Missed=%v", condDeny.Missed)
	}
}

// TestCoverageMultiConditionCreditsOnlyExercisedCondition guards the fix for
// per-relation condition over-crediting: a relation that declares two distinct
// conditions must credit only the one an assertion actually evaluated, not both.
func TestCoverageMultiConditionCreditsOnlyExercisedCondition(t *testing.T) {
	dir := t.TempDir()

	modelDSL := `model
  schema 1.1

type user

type doc
  relations
    define viewer: [user with cond_a, user with cond_b]

condition cond_a(a: int) {
  a > 0
}

condition cond_b(b: int) {
  b > 0
}
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	// Only cond_a is exercised (satisfied + violated); cond_b's tuple is never
	// present, so neither of its branches may be credited.
	testDSL := `model: ./model.fga
tests:
  - name: cond-a-only
    tuples:
      - user: user:anne
        relation: viewer
        object: doc:1
        condition: {name: cond_a}
    check:
      - user: user:anne
        object: doc:1
        context: {a: 5}
        assertions: {viewer: true}
      - user: user:anne
        object: doc:1
        context: {a: -1}
        assertions: {viewer: false}
`
	testPath := filepath.Join(dir, "cov.test.yaml")
	if err := os.WriteFile(testPath, []byte(testDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(testPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rc := findRelCov(res.Coverage, "doc", "viewer")
	if rc == nil {
		t.Fatal("expected doc#viewer in coverage")
	}

	// cond_a's outcomes were both exercised => covered.
	if missedContains(rc, "condition:cond_a=true") || missedContains(rc, "condition:cond_a=false") {
		t.Errorf("cond_a branches should both be covered, got Missed=%v", rc.Missed)
	}
	// cond_b was never evaluated => both branches MUST stay MISSED.
	if !missedContains(rc, "condition:cond_b=true") {
		t.Errorf("condition:cond_b=true must stay MISSED (never evaluated), got Missed=%v", rc.Missed)
	}
	if !missedContains(rc, "condition:cond_b=false") {
		t.Errorf("condition:cond_b=false must stay MISSED (never evaluated), got Missed=%v", rc.Missed)
	}
}

// TestCoverageBareAliasAndTTURelations covers the regression where a
// relation whose ENTIRE rewrite is a bare computed-userset alias (no union
// wrapper) or a bare tuple-to-userset pass-through has its root ExplainNode
// labeled with the TARGET's identity (nodeLabel's Computed/TTU branch), not
// its own. Keying coverage off that parsed label credited the wrong
// type#relation, leaving the alias/TTU relation itself reported as 0%
// covered even though it was exercised. Coverage must key off
// ExplainNode.Rel (the node's own identity) instead.
func TestCoverageBareAliasAndTTURelations(t *testing.T) {
	dir := t.TempDir()
	modelDSL := `model
  schema 1.1

type user

type folder
  relations
    define viewer: [user]

type document
  relations
    define parent: [folder]
    define owner: [user]
    define editor: owner
    define can_read: viewer from parent
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	testDSL := `model: ./model.fga
tests:
  - name: bare-alias-and-ttu
    tuples:
      - user: user:anne
        relation: owner
        object: document:1
      - user: folder:1
        relation: parent
        object: document:1
      - user: user:bob
        relation: viewer
        object: folder:1
    check:
      - user: user:anne
        object: document:1
        assertions: {editor: true}
      - user: user:bob
        object: document:1
        assertions: {can_read: true}
`
	testPath := filepath.Join(dir, "bare.test.yaml")
	if err := os.WriteFile(testPath, []byte(testDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(testPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage == nil {
		t.Fatal("want non-nil Coverage")
	}

	editorRC := findRelCov(res.Coverage, "document", "editor")
	if editorRC == nil {
		t.Fatal("expected document#editor in coverage")
	}
	if editorRC.Covered == 0 {
		t.Errorf("expected document#editor to have covered branches, got %+v", editorRC)
	}
	for _, m := range editorRC.Missed {
		if m == "computed:owner" {
			t.Errorf("expected computed:owner branch to be covered, found it in Missed: %+v", editorRC.Missed)
		}
	}

	canReadRC := findRelCov(res.Coverage, "document", "can_read")
	if canReadRC == nil {
		t.Fatal("expected document#can_read in coverage")
	}
	if canReadRC.Covered == 0 {
		t.Errorf("expected document#can_read to have covered branches, got %+v", canReadRC)
	}
	for _, m := range canReadRC.Missed {
		if m == "ttu:parent/viewer" {
			t.Errorf("expected ttu:parent/viewer branch to be covered, found it in Missed: %+v", canReadRC.Missed)
		}
	}
}

// TestCoverageListAssertionsCreditStructuralCoverage covers markStructuralCredit
// (run.go): a list_objects/list_users assertion has no resolution tree to
// trace, so branch coverage for a non-empty result can only come from the
// structured relation-granular credit on AssertionResult.covKey. The
// workspace's only assertions are a list_objects and a
// list_users case (no check assertions at all), so any coverage credit here
// can only have come from that path, not from resolvedRelations/Explain
// trees.
func TestCoverageListAssertionsCreditStructuralCoverage(t *testing.T) {
	dir := t.TempDir()

	modelDSL := `model
  schema 1.1

type user

type document
  relations
    define viewer: [user]

type folder
  relations
    define viewer: [user]
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	testDSL := `model: ./model.fga
tests:
  - name: list-objects-credits-coverage
    tuples:
      - user: user:anne
        relation: viewer
        object: document:1
    list_objects:
      - user: user:anne
        type: document
        assertions:
          viewer: [document:1]
  - name: list-users-credits-coverage
    tuples:
      - user: user:bob
        relation: viewer
        object: folder:1
    list_users:
      - object: folder:1
        user_filter:
          - type: user
        assertions:
          viewer: {users: [user:bob]}
`
	testPath := filepath.Join(dir, "cov.test.yaml")
	if err := os.WriteFile(testPath, []byte(testDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(testPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Failed != 0 {
		t.Fatalf("want 0 failed (real list_objects/list_users resolution), got %d: %+v", res.Summary.Failed, res.Tests)
	}
	if res.Coverage == nil {
		t.Fatal("want non-nil Coverage")
	}

	// [user] is the only directly-assignable type for either relation, so
	// enumerateBranches produces exactly one branch (direct:user) each; the
	// non-empty list assertion's structural credit must mark it granted.
	docRC := findRelCov(res.Coverage, "document", "viewer")
	if docRC == nil {
		t.Fatal("expected document#viewer in coverage")
	}
	if docRC.Total != 1 || docRC.Covered != 1 {
		t.Errorf("want document#viewer 1/1 covered via list_objects structural credit, got %+v", docRC)
	}

	folderRC := findRelCov(res.Coverage, "folder", "viewer")
	if folderRC == nil {
		t.Fatal("expected folder#viewer in coverage")
	}
	if folderRC.Total != 1 || folderRC.Covered != 1 {
		t.Errorf("want folder#viewer 1/1 covered via list_users structural credit, got %+v", folderRC)
	}
}

// TestAggregateBranchModePopulatesBranches checks that aggregate (branch
// mode) fills RelCov.Branches with one entry per enumerated branch, each
// carrying the right covered flag, not just the legacy Missed slice.
func TestAggregateBranchModePopulatesBranches(t *testing.T) {
	dsl := `model
  schema 1.1

type user

type document
  relations
    define banned: [user]
    define viewer: [user] but not banned
`
	lm := loadModelDSL(t, dsl)
	branches, unreachable := enumerateBranches(lm)

	resolved := map[string]*relOutcome{
		"document#viewer": {listGrant: true},
	}

	cov := aggregate(branches, unreachable, resolved)

	rc := findRelCov(cov, "document", "viewer")
	if rc == nil {
		t.Fatal("expected document#viewer in coverage")
	}
	if len(rc.Branches) != rc.Total {
		t.Fatalf("len(rc.Branches) = %d, want %d (Total)", len(rc.Branches), rc.Total)
	}
	for _, b := range rc.Branches {
		if !b.Covered {
			t.Errorf("expected branch %+v to be covered (document#viewer was resolved seen=true)", b)
		}
	}

	bannedRC := findRelCov(cov, "document", "banned")
	if bannedRC == nil {
		t.Fatal("expected document#banned in coverage")
	}
	for _, b := range bannedRC.Branches {
		if b.Covered {
			t.Errorf("expected branch %+v to be uncovered (document#banned was never resolved)", b)
		}
	}
}

func TestEmptyListResultDoesNotGrantCoverage(t *testing.T) {
	acc := map[string]*relOutcome{}
	markStructuralCredit(AssertionResult{
		Kind: kindListObjects, covKey: "document#viewer", Got: []string{},
	}, acc)
	if _, ok := acc["document#viewer"]; ok {
		t.Fatal("an empty list result proves denial and must not grant coverage")
	}
}

func TestAllUnreachableCoverageIsIncompleteAndZero(t *testing.T) {
	cov := aggregate(nil, []string{"document.viewer"}, nil)
	if cov.Complete {
		t.Fatal("coverage with unreachable relations must be incomplete")
	}
	if cov.Percent != 0 {
		t.Fatalf("all-unreachable coverage percent = %v, want 0", cov.Percent)
	}
}

// TestCoverageGrantBasedCreditsOnlyGrantingArms pins grant-based coverage: in a
// union, only the arm a test actually made grant is credited; a sibling arm the
// test never exercised stays Missed (under the old evaluated model both would
// have been credited once the relation was resolved at all).
func TestCoverageGrantBasedCreditsOnlyGrantingArms(t *testing.T) {
	dir := t.TempDir()
	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	// anne is an OWNER (so viewer grants via computed:owner), but no user is a
	// direct viewer — so direct:user must remain uncovered.
	testFile := "model: ./model.fga\n" +
		"tests:\n" +
		"  - name: owner-views\n" +
		"    tuples:\n" +
		"      - user:anne owner document:1\n" +
		"    check:\n" +
		"      - user: user:anne\n        object: document:1\n        assertions:\n          viewer: true\n"
	tp := filepath.Join(dir, "t.test.yaml")
	if err := os.WriteFile(tp, []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(tp)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := findRelCov(res.Coverage, "document", "viewer")
	if rc == nil {
		t.Fatal("want document#viewer in coverage")
	}
	covered := map[string]bool{}
	for _, b := range rc.Branches {
		covered[b.Label] = b.Covered
	}
	if !covered["computed:owner"] {
		t.Errorf("computed:owner should be covered (owner granted viewer), got %+v", rc.Branches)
	}
	if covered["direct:user"] {
		t.Errorf("direct:user should be uncovered (no direct viewer tuple was exercised), got %+v", rc.Branches)
	}
}

// TestCoverageGrantBasedIntersectionAndExclusion validates grant-based crediting
// on the two hardest rewrite forms: an intersection arm is covered only when it
// (jointly) granted, and a difference (but-not) arm is covered only when the
// subtract actually excluded.
func TestCoverageGrantBasedIntersectionAndExclusion(t *testing.T) {
	dir := t.TempDir()
	model := "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n" +
		"    define editor: [user]\n" +
		"    define approver: [user]\n" +
		"    define banned: [user]\n" +
		"    define publish: editor and approver\n" +
		"    define view: editor but not banned\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	// anne is editor+approver (so publish grants via BOTH intersection arms) and
	// editor but banned (so view is DENIED — the but-not arm actually excludes).
	testFile := "model: ./model.fga\n" +
		"tests:\n" +
		"  - name: t\n" +
		"    tuples:\n" +
		"      - user:anne editor doc:1\n" +
		"      - user:anne approver doc:1\n" +
		"      - user:anne banned doc:1\n" +
		"    check:\n" +
		"      - user: user:anne\n        object: doc:1\n        assertions:\n          publish: true\n          view: false\n"
	tp := filepath.Join(dir, "t.test.yaml")
	if err := os.WriteFile(tp, []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(tp)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	covered := func(rel string) map[string]bool {
		rc := findRelCov(res.Coverage, "doc", rel)
		if rc == nil {
			t.Fatalf("want doc#%s in coverage", rel)
		}
		m := map[string]bool{}
		for _, b := range rc.Branches {
			m[b.Label] = b.Covered
		}
		return m
	}

	// Intersection: both arms granted jointly, so both are covered.
	pub := covered("publish")
	if !pub["computed:editor"] || !pub["computed:approver"] {
		t.Errorf("both intersection arms should be covered when publish granted, got %+v", pub)
	}
	// Exclusion: the check was DENIED because banned excluded anne, so the
	// but-not arm was exercised and is covered.
	vw := covered("view")
	subtractCovered := false
	for label, ok := range vw {
		if strings.HasPrefix(label, "but-not:") && ok {
			subtractCovered = true
		}
	}
	if !subtractCovered {
		t.Errorf("the but-not (difference-subtract) arm should be covered when the exclusion excluded, got %+v", vw)
	}
}
