package playground

import (
	"context"
	"encoding/json"

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

type assertResult struct {
	label    string
	expected bool
	got      bool
	pass     bool
}

type storeCreatedMsg struct {
	store openfga.Store
	err   error
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
		m, _, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{err: err}
		}
		return modelLoadedMsg{modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m)}
	}
}

func loadModelByIDCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string) tea.Cmd {
	return func() tea.Msg {
		m, _, err := cl.AuthorizationModels.Get(ctx, modelID, openfga.WithStore(storeID))
		if err != nil {
			return modelLoadedMsg{err: err}
		}
		return modelLoadedMsg{modelID: m.ID, graph: fga.ParseModel(m), dsl: modelToDSL(m)}
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
			m, _, err := cl.AuthorizationModels.ReadLatest(ctx, openfga.WithStore(storeID))
			if err != nil {
				return assertionsLoadedMsg{err: err}
			}
			id = m.ID
		}
		res, _, err := cl.Assertions.Read(ctx, id, openfga.WithStore(storeID))
		if err != nil {
			return assertionsLoadedMsg{modelID: id, err: err}
		}
		return assertionsLoadedMsg{modelID: id, assertions: res.Assertions}
	}
}

func runAssertionsCmd(ctx context.Context, cl *openfga.Client, storeID, modelID string, assertions []openfga.Assertion) tea.Cmd {
	return func() tea.Msg {
		var results []assertResult
		passed := 0
		for _, a := range assertions {
			var ct *openfga.ContextualTupleKeys
			if len(a.ContextualTuples) > 0 {
				ct = &openfga.ContextualTupleKeys{TupleKeys: a.ContextualTuples}
			}
			cr, _, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
				TupleKey: a.TupleKey, ContextualTuples: ct, Context: a.Context,
			}, openfga.WithStore(storeID), openfga.WithAuthorizationModel(modelID))
			if err != nil {
				return assertTestMsg{err: err}
			}
			pass := cr.Allowed == a.Expectation
			if pass {
				passed++
			}
			results = append(results, assertResult{
				label:    a.TupleKey.User + " " + a.TupleKey.Relation + " " + a.TupleKey.Object,
				expected: a.Expectation, got: cr.Allowed, pass: pass,
			})
		}
		return assertTestMsg{results: results, passed: passed, total: len(assertions)}
	}
}

func createStoreCmd(ctx context.Context, cl *openfga.Client, name string) tea.Cmd {
	return func() tea.Msg {
		st, _, err := cl.Stores.Create(ctx, &openfga.CreateStoreRequest{Name: name})
		if err != nil {
			return storeCreatedMsg{err: err}
		}
		return storeCreatedMsg{store: *st}
	}
}

func writeTupleCmd(ctx context.Context, cl *openfga.Client, storeID string, key openfga.TupleKey, del bool) tea.Cmd {
	return func() tea.Msg {
		var req *openfga.WriteRequest
		if del {
			req = &openfga.WriteRequest{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		} else {
			req = &openfga.WriteRequest{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
		}
		if _, err := cl.Tuples.Write(ctx, req, openfga.WithStore(storeID)); err != nil {
			return tupleWrittenMsg{err: err, deleted: del}
		}
		return tupleWrittenMsg{label: fga.FormatTuple(key), deleted: del}
	}
}

func checkCmd(ctx context.Context, cl *openfga.Client, storeID, user, relation, object string) tea.Cmd {
	return func() tea.Msg {
		res, _, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
			TupleKey: openfga.CheckRequestTupleKey{User: user, Relation: relation, Object: object},
		}, openfga.WithStore(storeID))
		if err != nil {
			return queryResultMsg{err: err}
		}
		lines := []string{user + " " + relation + " " + object}
		if res.Resolution != "" {
			lines = append(lines, "resolution: "+res.Resolution)
		}
		return queryResultMsg{title: "Check", lines: lines, ok: res.Allowed, badge: true}
	}
}

func listObjectsCmd(ctx context.Context, cl *openfga.Client, storeID, typ, relation, user string) tea.Cmd {
	return func() tea.Msg {
		res, _, err := cl.Relationships.ListObjects(ctx, &openfga.ListObjectsRequest{
			Type: typ, Relation: relation, User: user,
		}, openfga.WithStore(storeID))
		if err != nil {
			return queryResultMsg{err: err}
		}
		title := user + " can " + relation + " these " + typ + " objects:"
		if len(res.Objects) == 0 {
			return queryResultMsg{title: title, lines: []string{"(none)"}}
		}
		return queryResultMsg{title: title, lines: res.Objects}
	}
}

func listUsersCmd(ctx context.Context, cl *openfga.Client, storeID, object, relation, userType string) tea.Cmd {
	return func() tea.Msg {
		res, _, err := cl.Relationships.ListUsers(ctx, &openfga.ListUsersRequest{
			Object:      openfga.FGAObjectRelation{Object: object},
			Relation:    relation,
			UserFilters: []openfga.UserTypeFilter{{Type: userType}},
		}, openfga.WithStore(storeID))
		if err != nil {
			return queryResultMsg{err: err}
		}
		title := "users with " + relation + " on " + object + ":"
		if len(res.Users) == 0 {
			return queryResultMsg{title: title, lines: []string{"(none)"}}
		}
		lines := make([]string, 0, len(res.Users))
		for _, u := range res.Users {
			lines = append(lines, formatUserEntry(u))
		}
		return queryResultMsg{title: title, lines: lines}
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
		res, _, err := cl.AuthorizationModels.Write(ctx, &req, openfga.WithStore(storeID))
		if err != nil {
			return modelAppliedMsg{err: err}
		}
		return modelAppliedMsg{modelID: res.AuthorizationModelID}
	}
}

func formatUserEntry(u map[string]any) string {
	if obj, ok := u["object"].(map[string]any); ok {
		t, _ := obj["type"].(string)
		id, _ := obj["id"].(string)
		return t + ":" + id
	}
	if us, ok := u["userset"].(map[string]any); ok {
		if obj, ok := us["object"].(map[string]any); ok {
			t, _ := obj["type"].(string)
			id, _ := obj["id"].(string)
			rel, _ := us["relation"].(string)
			return t + ":" + id + "#" + rel
		}
	}
	if w, ok := u["wildcard"].(map[string]any); ok {
		t, _ := w["type"].(string)
		return t + ":*"
	}
	return "?"
}
