package playground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	transformer "github.com/openfga/language/pkg/go/transformer"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/fga"
)

// Display caps: the playground loads at most this many rows for the tuples and
// changes panes. A note is shown when the cap hides further rows.
const (
	tuplesDisplayCap  = 500
	changesDisplayCap = 200
)

// --- async messages ---

// bootNoticeMsg carries a one-time startup notice to the Update loop, where the
// toast can actually be pushed (Init's receiver mutations are discarded).
type bootNoticeMsg struct{ text string }

type storesLoadedMsg struct {
	gen int // stores-list generation the load ran under, to drop a response
	// superseded by a newer stores dispatch — a reconnect (profile switch or
	// active-profile edit), or another same-connection refresh (manual reload,
	// create/delete) — see Model.storesGen
	stores []openfga.Store
	err    error
}

type modelLoadedMsg struct {
	storeID string // store the load ran against, to drop results from a stale store
	gen     int    // request generation the load ran under, to drop a response
	// superseded by a newer model request against the same store (e.g. two
	// quick picks in the model switcher, or "r" pressed twice in a row)
	modelID string
	graph   fga.Graph
	dsl     string
	latest  bool // loaded via ReadLatest (i.e. the store's newest model)
	err     error
}

type modelsListedMsg struct {
	storeID string // store the load ran against, to drop results from a stale store
	gen     int    // request generation the load ran under, to drop a response
	// superseded by a newer list request against the same store (e.g. the
	// model switcher closed and reopened before the first list landed) — kept
	// distinct from modelGen since picking a model must not invalidate an
	// in-flight list request, and vice versa
	models []openfga.AuthorizationModel
	err    error
}

type tuplesLoadedMsg struct {
	storeID string // store the load ran against, to drop results from a stale store
	gen     int    // request generation the load ran under, to drop a response
	// superseded by a newer tuples load against the same store (e.g. a manual
	// reload racing the reload a tuple write already triggers)
	tuples []openfga.Tuple
	capped bool // more rows exist than were loaded (hit the display cap)
	err    error
}

type changesLoadedMsg struct {
	storeID string // store the load ran against, to drop results from a stale store
	gen     int    // request generation the load ran under, to drop a response
	// superseded by a newer changes load against the same store (e.g. a manual
	// reload racing the lazy first-entry load)
	changes []openfga.TupleChange
	capped  bool // more changes exist than were loaded (hit the display cap)
	err     error
}

type assertionsLoadedMsg struct {
	storeID string // store the load ran against, to drop results from a stale store
	modelID string // model the load resolved to, to drop a response for a model
	// that stopped being current before it landed (see resolveAssertModel)
	gen int // request generation the load ran under, to drop a response
	// superseded by a newer assertions load against the same store/model (e.g.
	// a manual reload racing the reload a write already triggers) — kept
	// distinct from assertGen (assertion runs) since those are different
	// requests entirely
	assertions []openfga.Assertion
	err        error
}

type assertTestMsg struct {
	storeID string // store the run ran against, to drop a stale response
	modelID string // model the run ran against, to drop a stale response
	gen     int    // request generation, to drop a response superseded by a
	// newer assertion run (single or full) against the same store/model
	results []assertResult
	passed  int
	total   int
	err     error
}

// assertOneMsg carries the result of running a single assertion by index.
type assertOneMsg struct {
	storeID string // store the run ran against, to drop a stale response
	modelID string // model the run ran against, to drop a stale response
	gen     int    // request generation, to drop a response superseded by a
	// newer assertion run against the same store/model
	idx    int
	result assertResult
	err    error
}

// mutationOrigin identifies the live connection and resource selection that
// dispatched a mutation. A completion carrying an older origin is ignored.
type mutationOrigin struct {
	connGen int
	gen     int
	profile string
	storeID string
	modelID string
}

// assertionsWrittenMsg reports the outcome of replacing a model's assertion set.
type assertionsWrittenMsg struct {
	origin  mutationOrigin
	modelID string
	err     error
}

// resolutionMsg carries the parsed Expand (userset) tree for a check.
type resolutionMsg struct {
	storeID string // store the load ran against, to drop a stale response
	modelID string // model the load ran against, to drop a stale response
	gen     int    // request generation, to drop a response superseded by a
	// newer resolution request against the same store/model
	root *fga.ResNode
	err  error
}

type assertResult struct {
	ran      bool
	expected bool
	got      bool
	pass     bool
	label    string
}

type storeCreatedMsg struct {
	origin mutationOrigin
	store  openfga.Store
	err    error
}

type storeDeletedMsg struct {
	origin mutationOrigin
	id     string
	err    error
}

type modelAppliedMsg struct {
	origin  mutationOrigin
	modelID string
	err     error
}

type tupleWrittenMsg struct {
	origin  mutationOrigin
	label   string
	deleted bool
	err     error
}

type queryResultMsg struct {
	storeID string // store the query ran against, to drop a stale response
	modelID string // model the query ran against, to drop a stale response
	gen     int    // request generation, to drop a response superseded by a
	// newer query submission or rerun against the same store/model
	title string
	lines []string
	ok    bool
	badge bool
	err   error
	ms    int64     // latency of the query, from command build to result
	vals  [3]string // the three field values the query ran with, in form order
	mode  string    // mode the query ran under (from queryModes)
	qctx  queryCtx  // ABAC context + contextual tuples the query ran with (for history rerun)
}

// --- command builders ---

func loadStoresCmd(ctx context.Context, cl *openfga.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		var stores []openfga.Store
		for st, err := range cl.Stores.All(ctx, nil) {
			if err != nil {
				return storesLoadedMsg{gen: gen, err: err}
			}
			stores = append(stores, st)
		}
		return storesLoadedMsg{gen: gen, stores: stores}
	}
}

func modelToDSL(m *openfga.AuthorizationModel) string {
	if m == nil {
		return ""
	}
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	dsl, err := transformer.TransformJSONStringToDSL(string(jsonBytes))
	if err != nil || dsl == nil {
		return ""
	}
	return *dsl
}

func loadModelCmd(ctx context.Context, cl *openfga.Client, storeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{storeID: storeID, gen: gen, err: err}
		}
		return modelLoadedMsg{storeID: storeID, gen: gen, modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m), latest: true}
	}
}

func loadModelByIDCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, gen int) tea.Cmd {
	return func() tea.Msg {
		m, err := cl.AuthorizationModels.Get(ctx, modelID, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{storeID: storeID, gen: gen, err: err}
		}
		// Learn whether this model is the store's newest so the footer's
		// "(latest)" tag is correct even before the full model list is loaded
		// (e.g. when restoring a persisted model on startup).
		latest := false
		if newest, lerr := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID)); lerr == nil {
			latest = newest.ID == m.ID
		}
		return modelLoadedMsg{storeID: storeID, gen: gen, modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m), latest: latest}
	}
}

func loadModelsCmd(ctx context.Context, cl *openfga.Client, storeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		var models []openfga.AuthorizationModel
		for m, err := range cl.AuthorizationModels.All(ctx, nil, openfga.WithStore(storeID)) {
			if err != nil {
				return modelsListedMsg{storeID: storeID, gen: gen, err: err}
			}
			models = append(models, m)
		}
		return modelsListedMsg{storeID: storeID, gen: gen, models: models}
	}
}

func loadTuplesCmd(ctx context.Context, cl *openfga.Client, storeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		var tuples []openfga.Tuple
		req := &openfga.ReadRequest{PageSize: 100}
		capped := false
		for t, err := range cl.Tuples.ReadAll(ctx, req, openfga.WithStore(storeID)) {
			if err != nil {
				return tuplesLoadedMsg{storeID: storeID, gen: gen, err: err}
			}
			if len(tuples) >= tuplesDisplayCap {
				capped = true // at least one more row exists beyond the cap
				break
			}
			tuples = append(tuples, t)
		}
		return tuplesLoadedMsg{storeID: storeID, gen: gen, tuples: tuples, capped: capped}
	}
}

func loadChangesCmd(ctx context.Context, cl *openfga.Client, storeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		var changes []openfga.TupleChange
		capped := false
		for ch, err := range cl.Tuples.ChangesAll(ctx, &openfga.ReadChangesOptions{}, openfga.WithStore(storeID)) {
			if err != nil {
				return changesLoadedMsg{storeID: storeID, gen: gen, err: err}
			}
			if len(changes) >= changesDisplayCap {
				capped = true
				break
			}
			changes = append(changes, ch)
		}
		return changesLoadedMsg{storeID: storeID, gen: gen, changes: changes, capped: capped}
	}
}

func loadAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, gen int) tea.Cmd {
	return func() tea.Msg {
		id := modelID
		if id == "" {
			m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
			if err != nil {
				return assertionsLoadedMsg{storeID: storeID, gen: gen, err: err}
			}
			id = m.ID
		}
		res, err := cl.Assertions.Read(ctx, id, openfga.WithStore(storeID))
		if err != nil {
			return assertionsLoadedMsg{storeID: storeID, modelID: id, gen: gen, err: err}
		}
		return assertionsLoadedMsg{storeID: storeID, modelID: id, gen: gen, assertions: res.Assertions}
	}
}

// checkAssertion runs a single assertion's Check and scores it against the
// expectation.
func checkAssertion(ctx context.Context, cl *openfga.Client, storeID, modelID string, a openfga.Assertion) (assertResult, error) {
	var ct *openfga.ContextualTupleKeys
	if len(a.ContextualTuples) > 0 {
		ct = &openfga.ContextualTupleKeys{TupleKeys: a.ContextualTuples}
	}
	cr, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
		TupleKey: a.TupleKey, ContextualTuples: ct, Context: a.Context,
	}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
	if err != nil {
		return assertResult{}, err
	}
	return assertResult{
		ran:      true,
		label:    a.TupleKey.User + " " + a.TupleKey.Relation + " " + a.TupleKey.Object,
		expected: a.Expectation, got: cr.Allowed, pass: cr.Allowed == a.Expectation,
	}, nil
}

func runAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, assertions []openfga.Assertion, gen int) tea.Cmd {
	return func() tea.Msg {
		results := make([]assertResult, len(assertions))
		passed := 0
		for i, a := range assertions {
			r, err := checkAssertion(ctx, cl, storeID, modelID, a)
			if err != nil {
				return assertTestMsg{storeID: storeID, modelID: modelID, gen: gen, err: err}
			}
			results[i] = r
			if r.pass {
				passed++
			}
		}
		return assertTestMsg{storeID: storeID, modelID: modelID, gen: gen, results: results, passed: passed, total: len(assertions)}
	}
}

// runOneAssertionCmd runs a single assertion by index and reports its result.
func runOneAssertionCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, idx int, a openfga.Assertion, gen int) tea.Cmd {
	return func() tea.Msg {
		r, err := checkAssertion(ctx, cl, storeID, modelID, a)
		if err != nil {
			return assertOneMsg{storeID: storeID, modelID: modelID, gen: gen, idx: idx, err: err}
		}
		return assertOneMsg{storeID: storeID, modelID: modelID, gen: gen, idx: idx, result: r}
	}
}

// writeAssertionsCmd replaces the model's whole assertion set (the OpenFGA API
// is a full PUT), then the caller reloads to confirm.
func writeAssertionsCmd(ctx context.Context, cl *openfga.Client, origin mutationOrigin, assertions []openfga.Assertion) tea.Cmd {
	return func() tea.Msg {
		id := origin.modelID
		if id == "" {
			m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(origin.storeID))
			if err != nil {
				return assertionsWrittenMsg{origin: origin, err: err}
			}
			id = m.ID
		}
		req := &openfga.WriteAssertionsRequest{Assertions: assertions}
		if err := cl.Assertions.Write(ctx, id, req, openfga.WithStore(origin.storeID)); err != nil {
			return assertionsWrittenMsg{origin: origin, modelID: id, err: err}
		}
		return assertionsWrittenMsg{origin: origin, modelID: id}
	}
}

func createStoreCmd(ctx context.Context, cl *openfga.Client, origin mutationOrigin, name string) tea.Cmd {
	return func() tea.Msg {
		st, err := cl.Stores.Create(ctx, &openfga.CreateStoreRequest{Name: name})
		if err != nil {
			return storeCreatedMsg{origin: origin, err: err}
		}
		return storeCreatedMsg{origin: origin, store: *st}
	}
}

func deleteStoreCmd(ctx context.Context, cl *openfga.Client, origin mutationOrigin, id string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Stores.Delete(ctx, id); err != nil {
			return storeDeletedMsg{origin: origin, id: id, err: err}
		}
		return storeDeletedMsg{origin: origin, id: id}
	}
}

func writeTupleCmd(ctx context.Context, cl *openfga.Client, origin mutationOrigin, key openfga.TupleKey, del bool) tea.Cmd {
	return func() tea.Msg {
		var req *openfga.WriteRequest
		if del {
			req = &openfga.WriteRequest{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		} else {
			req = &openfga.WriteRequest{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		}
		if err := cl.Tuples.Write(ctx, req, openfga.WithStore(origin.storeID), openfga.WithAuthorizationModel(origin.modelID)); err != nil {
			return tupleWrittenMsg{origin: origin, err: err, deleted: del}
		}
		return tupleWrittenMsg{origin: origin, label: fga.FormatTuple(key), deleted: del}
	}
}

// expandCmd fetches the Expand (userset) tree for object#relation, parses it,
// and marks the branch that grants `user` (resolving computed usersets with
// live Checks) so the tree can highlight the resolution path.
// Recursive resolution safety bounds: how deep ExpandTree recurses and how many
// extra Expand calls it may make in total, so a deep or cyclic model can't fan
// the tree (and the API traffic) out without limit.
const (
	resolveMaxDepth = 8
	resolveMaxNodes = 64
)

func expandCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, user, relation, object string, gen int) tea.Cmd {
	return func() tea.Msg {
		// tupleset lists the objects related to `object` via `relation` — the
		// "user" side of matching tuples — used both to expand tuple-to-userset
		// branches and to resolve them in MarkGranted.
		tupleset := func(object, relation string) []string {
			var xs []string
			req := &openfga.ReadRequest{
				TupleKey: &openfga.ReadRequestTupleKey{Object: object, Relation: relation},
				PageSize: 100,
			}
			for tp, terr := range cl.Tuples.ReadAll(ctx, req, openfga.WithStore(storeID)) {
				if terr != nil {
					break
				}
				xs = append(xs, tp.Key.User)
				if len(xs) >= 100 {
					break
				}
			}
			return xs
		}
		// expand fetches one level of resolution for object#relation, so
		// ExpandTree can splice nested branches in place of dead-end leaves.
		expand := func(obj, rel string) *fga.ResNode {
			er, eerr := cl.Relationships.Expand(ctx, &openfga.ExpandRequest{
				TupleKey: openfga.CheckRequestTupleKey{Relation: rel, Object: obj},
			}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
			if eerr != nil {
				return nil
			}
			sub, ok := fga.ParseResolution(er.Tree)
			if !ok {
				return nil
			}
			return sub
		}

		root := expand(object, relation)
		if root == nil {
			return resolutionMsg{storeID: storeID, modelID: modelID, gen: gen, err: errors.New("empty resolution tree")}
		}
		// Recursively resolve computed-userset and tuple-to-userset leaves so the
		// tree shows nested branches, not one-level dead ends.
		fga.ExpandTree(root, object+"#"+relation, expand, tupleset, resolveMaxDepth, resolveMaxNodes)

		fga.MarkGranted(root, user, fga.GrantResolver{
			Check: func(u, rel, obj string) bool {
				cr, cerr := cl.Relationships.Check(ctx, &openfga.CheckRequest{
					TupleKey: openfga.CheckRequestTupleKey{User: u, Relation: rel, Object: obj},
				}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
				return cerr == nil && cr.Allowed
			},
			Tupleset: tupleset,
		})
		return resolutionMsg{storeID: storeID, modelID: modelID, gen: gen, root: root}
	}
}

func checkCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, user, relation, object string, qc queryCtx, gen int) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
			TupleKey:         openfga.CheckRequestTupleKey{User: user, Relation: relation, Object: object},
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, err: err, ms: ms}
		}
		lines := []string{user + " " + relation + " " + object}
		if res.Resolution != "" {
			lines = append(lines, "resolution: "+res.Resolution)
		}
		return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: "Check", lines: lines, ok: res.Allowed, badge: true, ms: ms, vals: [3]string{user, relation, object}, mode: "check", qctx: qc}
	}
}

func listObjectsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, typ, relation, user string, qc queryCtx, gen int) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.ListObjects(ctx, &openfga.ListObjectsRequest{
			Type: typ, Relation: relation, User: user,
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, err: err, ms: ms}
		}
		title := user + " can " + relation + " these " + typ + " objects:"
		vals := [3]string{typ, relation, user}
		if len(res.Objects) == 0 {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: []string{"(none)"}, ms: ms, vals: vals, mode: "list-objects", qctx: qc}
		}
		return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: res.Objects, ms: ms, vals: vals, mode: "list-objects", qctx: qc}
	}
}

func listUsersCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, object, relation, userType string, qc queryCtx, gen int) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.ListUsers(ctx, &openfga.ListUsersRequest{
			Object:           openfga.FGAObjectRelation{Object: object},
			Relation:         relation,
			UserFilters:      []openfga.UserTypeFilter{{Type: userType}},
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, err: err, ms: ms}
		}
		title := "users with " + relation + " on " + object + ":"
		vals := [3]string{object, relation, userType}
		if len(res.Users) == 0 {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: []string{"(none)"}, ms: ms, vals: vals, mode: "list-users", qctx: qc}
		}
		lines := make([]string, 0, len(res.Users))
		for _, u := range res.Users {
			lines = append(lines, formatUserEntry(u))
		}
		return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: lines, ms: ms, vals: vals, mode: "list-users", qctx: qc}
	}
}

// relationsForType returns every relation defined on the type of object (the
// part before the ":") in the parsed model, in the graph's sorted order. It
// backs list-relations, which tests a user against all of them. It errors when
// object carries no type, the type is absent from the model, or the type has no
// relations — none of which yield anything to test.
func relationsForType(g fga.Graph, object string) ([]string, error) {
	typ, _, ok := strings.Cut(object, ":")
	if !ok || typ == "" {
		return nil, fmt.Errorf("object %q has no type (expected type:id)", object)
	}
	for _, t := range g.Types {
		if t.Name != typ {
			continue
		}
		if len(t.Relations) == 0 {
			return nil, fmt.Errorf("type %q has no relations to test", typ)
		}
		rels := make([]string, len(t.Relations))
		for i, r := range t.Relations {
			rels[i] = r.Name
		}
		return rels, nil
	}
	return nil, fmt.Errorf("type %q is not defined in the model", typ)
}

// listRelationsCmd tests user against each of relations on object and reports
// the ones that hold. The relations are resolved from the model by the caller
// so the command stays independent of the graph.
func listRelationsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, user, object string, relations []string, qc queryCtx, gen int) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.ListRelations(ctx, &openfga.ListRelationsRequest{
			User: user, Object: object, Relations: relations,
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, err: err, ms: ms}
		}
		title := user + " has these relations on " + object + ":"
		vals := [3]string{user, object, ""}
		if len(res) == 0 {
			return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: []string{"(none)"}, ms: ms, vals: vals, mode: "list-relations", qctx: qc}
		}
		return queryResultMsg{storeID: storeID, modelID: modelID, gen: gen, title: title, lines: res, ms: ms, vals: vals, mode: "list-relations", qctx: qc}
	}
}

func applyModelCmd(ctx context.Context, cl *openfga.Client, origin mutationOrigin, dsl string) tea.Cmd {
	return func() tea.Msg {
		jsonStr, err := transformer.TransformDSLToJSON(dsl)
		if err != nil {
			return modelAppliedMsg{origin: origin, err: err}
		}
		var req openfga.WriteAuthorizationModelRequest
		if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
			return modelAppliedMsg{origin: origin, err: err}
		}
		res, err := cl.AuthorizationModels.Write(ctx, &req, openfga.WithStore(origin.storeID))
		if err != nil {
			return modelAppliedMsg{origin: origin, err: err}
		}
		return modelAppliedMsg{origin: origin, modelID: res.AuthorizationModelID}
	}
}

func formatUserEntry(u openfga.User) string {
	switch {
	case u.Object != nil:
		return u.Object.Type + ":" + u.Object.ID
	case u.Userset != nil:
		return u.Userset.Type + ":" + u.Userset.ID + "#" + u.Userset.Relation
	case u.Wildcard != nil:
		return u.Wildcard.Type + ":*"
	default:
		return "?"
	}
}
