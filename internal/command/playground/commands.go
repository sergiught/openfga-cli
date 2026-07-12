package playground

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	transformer "github.com/openfga/language/pkg/go/transformer"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/fga"
)

// --- async messages ---

type storesLoadedMsg struct {
	stores []openfga.Store
	err    error
}

type modelLoadedMsg struct {
	modelID string
	graph   fga.Graph
	dsl     string
	latest  bool // loaded via ReadLatest (i.e. the store's newest model)
	err     error
}

type modelsListedMsg struct {
	models []openfga.AuthorizationModel
	err    error
}

type tuplesLoadedMsg struct {
	tuples []openfga.Tuple
	err    error
}

type changesLoadedMsg struct {
	changes []openfga.TupleChange
	err     error
}

type assertionsLoadedMsg struct {
	modelID    string
	assertions []openfga.Assertion
	err        error
}

type assertTestMsg struct {
	results []assertResult
	passed  int
	total   int
	err     error
}

// assertOneMsg carries the result of running a single assertion by index.
type assertOneMsg struct {
	idx    int
	result assertResult
	err    error
}

// assertionsWrittenMsg reports the outcome of replacing a model's assertion set.
type assertionsWrittenMsg struct {
	modelID string
	err     error
}

// resolutionMsg carries the parsed Expand (userset) tree for a check.
type resolutionMsg struct {
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
	store openfga.Store
	err   error
}

type storeDeletedMsg struct {
	id  string
	err error
}

type modelAppliedMsg struct {
	modelID string
	err     error
}

type tupleWrittenMsg struct {
	label   string
	deleted bool
	err     error
}

type queryResultMsg struct {
	title string
	lines []string
	ok    bool
	badge bool
	err   error
	ms    int64     // latency of the query, from command build to result
	vals  [3]string // the three field values the query ran with, in form order
	mode  string    // mode the query ran under (from queryModes)
}

// --- command builders ---

func loadStoresCmd(ctx context.Context, cl *openfga.Client) tea.Cmd {
	return func() tea.Msg {
		var stores []openfga.Store
		for st, err := range cl.Stores.All(ctx, nil) {
			if err != nil {
				return storesLoadedMsg{err: err}
			}
			stores = append(stores, st)
		}
		return storesLoadedMsg{stores: stores}
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

func loadModelCmd(ctx context.Context, cl *openfga.Client, storeID string) tea.Cmd {
	return func() tea.Msg {
		m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{err: err}
		}
		return modelLoadedMsg{modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m), latest: true}
	}
}

func loadModelByIDCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string) tea.Cmd {
	return func() tea.Msg {
		m, err := cl.AuthorizationModels.Get(ctx, modelID, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{err: err}
		}
		// Learn whether this model is the store's newest so the footer's
		// "(latest)" tag is correct even before the full model list is loaded
		// (e.g. when restoring a persisted model on startup).
		latest := false
		if newest, lerr := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID)); lerr == nil {
			latest = newest.ID == m.ID
		}
		return modelLoadedMsg{modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m), latest: latest}
	}
}

func loadModelsCmd(ctx context.Context, cl *openfga.Client, storeID string) tea.Cmd {
	return func() tea.Msg {
		var models []openfga.AuthorizationModel
		for m, err := range cl.AuthorizationModels.All(ctx, nil, openfga.WithStore(storeID)) {
			if err != nil {
				return modelsListedMsg{err: err}
			}
			models = append(models, m)
		}
		return modelsListedMsg{models: models}
	}
}

func loadTuplesCmd(ctx context.Context, cl *openfga.Client, storeID string) tea.Cmd {
	return func() tea.Msg {
		var tuples []openfga.Tuple
		req := &openfga.ReadRequest{PageSize: 100}
		count := 0
		for t, err := range cl.Tuples.ReadAll(ctx, req, openfga.WithStore(storeID)) {
			if err != nil {
				return tuplesLoadedMsg{err: err}
			}
			tuples = append(tuples, t)
			if count++; count >= 500 {
				break
			}
		}
		return tuplesLoadedMsg{tuples: tuples}
	}
}

func loadChangesCmd(ctx context.Context, cl *openfga.Client, storeID string) tea.Cmd {
	return func() tea.Msg {
		var changes []openfga.TupleChange
		count := 0
		for ch, err := range cl.Tuples.ChangesAll(ctx, &openfga.ReadChangesOptions{}, openfga.WithStore(storeID)) {
			if err != nil {
				return changesLoadedMsg{err: err}
			}
			changes = append(changes, ch)
			if count++; count >= 200 {
				break
			}
		}
		return changesLoadedMsg{changes: changes}
	}
}

func loadAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string) tea.Cmd {
	return func() tea.Msg {
		id := modelID
		if id == "" {
			m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
			if err != nil {
				return assertionsLoadedMsg{err: err}
			}
			id = m.ID
		}
		res, err := cl.Assertions.Read(ctx, id, openfga.WithStore(storeID))
		if err != nil {
			return assertionsLoadedMsg{modelID: id, err: err}
		}
		return assertionsLoadedMsg{modelID: id, assertions: res.Assertions}
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

func runAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, assertions []openfga.Assertion) tea.Cmd {
	return func() tea.Msg {
		results := make([]assertResult, len(assertions))
		passed := 0
		for i, a := range assertions {
			r, err := checkAssertion(ctx, cl, storeID, modelID, a)
			if err != nil {
				return assertTestMsg{err: err}
			}
			results[i] = r
			if r.pass {
				passed++
			}
		}
		return assertTestMsg{results: results, passed: passed, total: len(assertions)}
	}
}

// runOneAssertionCmd runs a single assertion by index and reports its result.
func runOneAssertionCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, idx int, a openfga.Assertion) tea.Cmd {
	return func() tea.Msg {
		r, err := checkAssertion(ctx, cl, storeID, modelID, a)
		if err != nil {
			return assertOneMsg{idx: idx, err: err}
		}
		return assertOneMsg{idx: idx, result: r}
	}
}

// writeAssertionsCmd replaces the model's whole assertion set (the OpenFGA API
// is a full PUT), then the caller reloads to confirm.
func writeAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, assertions []openfga.Assertion) tea.Cmd {
	return func() tea.Msg {
		id := modelID
		if id == "" {
			m, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
			if err != nil {
				return assertionsWrittenMsg{err: err}
			}
			id = m.ID
		}
		req := &openfga.WriteAssertionsRequest{Assertions: assertions}
		if err := cl.Assertions.Write(ctx, id, req, openfga.WithStore(storeID)); err != nil {
			return assertionsWrittenMsg{modelID: id, err: err}
		}
		return assertionsWrittenMsg{modelID: id}
	}
}

func createStoreCmd(ctx context.Context, cl *openfga.Client, name string) tea.Cmd {
	return func() tea.Msg {
		st, err := cl.Stores.Create(ctx, &openfga.CreateStoreRequest{Name: name})
		if err != nil {
			return storeCreatedMsg{err: err}
		}
		return storeCreatedMsg{store: *st}
	}
}

func deleteStoreCmd(ctx context.Context, cl *openfga.Client, id string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Stores.Delete(ctx, id); err != nil {
			return storeDeletedMsg{id: id, err: err}
		}
		return storeDeletedMsg{id: id}
	}
}

func writeTupleCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, key openfga.TupleKey, del bool) tea.Cmd {
	return func() tea.Msg {
		var req *openfga.WriteRequest
		if del {
			req = &openfga.WriteRequest{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		} else {
			req = &openfga.WriteRequest{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		}
		if err := cl.Tuples.Write(ctx, req, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID)); err != nil {
			return tupleWrittenMsg{err: err, deleted: del}
		}
		return tupleWrittenMsg{label: fga.FormatTuple(key), deleted: del}
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

func expandCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, user, relation, object string) tea.Cmd {
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
			return resolutionMsg{err: errors.New("empty resolution tree")}
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
		return resolutionMsg{root: root}
	}
}

func checkCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, user, relation, object string, qc queryCtx) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
			TupleKey:         openfga.CheckRequestTupleKey{User: user, Relation: relation, Object: object},
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{err: err, ms: ms}
		}
		lines := []string{user + " " + relation + " " + object}
		if res.Resolution != "" {
			lines = append(lines, "resolution: "+res.Resolution)
		}
		return queryResultMsg{title: "Check", lines: lines, ok: res.Allowed, badge: true, ms: ms, vals: [3]string{user, relation, object}, mode: "check"}
	}
}

func listObjectsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, typ, relation, user string, qc queryCtx) tea.Cmd {
	start := time.Now()
	return func() tea.Msg {
		res, err := cl.Relationships.ListObjects(ctx, &openfga.ListObjectsRequest{
			Type: typ, Relation: relation, User: user,
			ContextualTuples: contextualTupleKeys(qc.contextual),
			Context:          qc.context,
		}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
		ms := time.Since(start).Milliseconds()
		if err != nil {
			return queryResultMsg{err: err, ms: ms}
		}
		title := user + " can " + relation + " these " + typ + " objects:"
		vals := [3]string{typ, relation, user}
		if len(res.Objects) == 0 {
			return queryResultMsg{title: title, lines: []string{"(none)"}, ms: ms, vals: vals, mode: "list-objects"}
		}
		return queryResultMsg{title: title, lines: res.Objects, ms: ms, vals: vals, mode: "list-objects"}
	}
}

func listUsersCmd(ctx context.Context, cl *openfga.Client, storeID, modelID, object, relation, userType string, qc queryCtx) tea.Cmd {
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
			return queryResultMsg{err: err, ms: ms}
		}
		title := "users with " + relation + " on " + object + ":"
		vals := [3]string{object, relation, userType}
		if len(res.Users) == 0 {
			return queryResultMsg{title: title, lines: []string{"(none)"}, ms: ms, vals: vals, mode: "list-users"}
		}
		lines := make([]string, 0, len(res.Users))
		for _, u := range res.Users {
			lines = append(lines, formatUserEntry(u))
		}
		return queryResultMsg{title: title, lines: lines, ms: ms, vals: vals, mode: "list-users"}
	}
}

func applyModelCmd(ctx context.Context, cl *openfga.Client, storeID, dsl string) tea.Cmd {
	return func() tea.Msg {
		jsonStr, err := transformer.TransformDSLToJSON(dsl)
		if err != nil {
			return modelAppliedMsg{err: err}
		}
		var req openfga.WriteAuthorizationModelRequest
		if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
			return modelAppliedMsg{err: err}
		}
		res, err := cl.AuthorizationModels.Write(ctx, &req, openfga.WithStore(storeID))
		if err != nil {
			return modelAppliedMsg{err: err}
		}
		return modelAppliedMsg{modelID: res.AuthorizationModelID}
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
