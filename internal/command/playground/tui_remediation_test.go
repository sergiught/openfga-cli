package playground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestMutationCompletionsDroppedAfterConnectionSwitch(t *testing.T) {
	old := mutationOrigin{connGen: 4, profile: "a", storeID: "store-1", modelID: "model-1"}
	base := newTestModel().(Model)
	base.connGen = 5
	base.profile = "b"
	base.storeID = "store-1"
	base.modelID = "model-1"

	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{"store create", storeCreatedMsg{origin: mutationOrigin{connGen: old.connGen, profile: old.profile}, store: openfga.Store{ID: "stale-store", Name: "stale"}}},
		{"store delete", storeDeletedMsg{origin: mutationOrigin{connGen: old.connGen, profile: old.profile}, id: "store-1"}},
		{"model apply", modelAppliedMsg{origin: mutationOrigin{connGen: old.connGen, profile: old.profile, storeID: old.storeID}, modelID: "stale-model"}},
		{"tuple write", tupleWrittenMsg{origin: old, label: "stale tuple"}},
		{"assertion write", assertionsWrittenMsg{origin: old, modelID: "model-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			m.storesGen, m.modelGen, m.tuplesGen, m.assertLoadGen = 10, 10, 10, 10
			beforeStore, beforeModel := m.storeID, m.modelID
			nm, cmd := m.Update(tt.msg)
			got := nm.(Model)
			if cmd != nil {
				t.Fatal("a stale mutation completion must not toast or trigger reloads")
			}
			if got.storeID != beforeStore || got.modelID != beforeModel {
				t.Fatalf("stale completion changed selection: store=%q model=%q", got.storeID, got.modelID)
			}
			if got.storesGen != 10 || got.modelGen != 10 || got.tuplesGen != 10 || got.assertLoadGen != 10 {
				t.Fatal("stale completion triggered a new-profile reload")
			}
		})
	}
}

func TestStoreCreateCompletionDroppedInProfileAToBRace(t *testing.T) {
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "http://server-a:8080"})
	cfg.Set("other", config.Profile{APIURL: "http://server-b:8080"})
	cl, _ := openfga.NewClient("http://server-a:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, "", "")
	oldOrigin := m.mutationOrigin("", "")

	_ = m.switchProfile("other")
	if m.profile != "other" || m.connGen == oldOrigin.connGen {
		t.Fatalf("precondition: A->B switch did not change identity: profile=%q gen=%d", m.profile, m.connGen)
	}
	nm, cmd := m.Update(storeCreatedMsg{
		origin: oldOrigin,
		store:  openfga.Store{ID: "server-a-store", Name: "stale"},
	})
	got := nm.(Model)
	if cmd != nil || got.storeID != "" {
		t.Fatalf("server A completion affected profile B: cmd=%v store=%q", cmd != nil, got.storeID)
	}
	if p, _ := cfg.Get("other"); p.StoreID != "" || p.ModelID != "" {
		t.Fatalf("server A IDs persisted onto profile B: %+v", p)
	}
}

func TestAssertionDeleteRevalidatesStableIdentity(t *testing.T) {
	a := openfga.Assertion{TupleKey: openfga.CheckRequestTupleKey{User: "user:a", Relation: "viewer", Object: "doc:1"}, Expectation: true}
	b := openfga.Assertion{TupleKey: openfga.CheckRequestTupleKey{User: "user:b", Relation: "viewer", Object: "doc:1"}}
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel().(Model)
	m.client = cl
	m.section = secAssertions
	m.focus = 1
	m.assertions = []openfga.Assertion{a, b}
	m.populateAssertions()

	nm, _ := m.handleSectionKey("d", key("d"))
	m = nm.(Model)
	// A reload reordered the slice while the confirmation was open.
	m.assertions = []openfga.Assertion{b, a}
	m.populateAssertions()
	nm, cmd := m.handleKey(key("y"))
	m = nm.(Model)
	if cmd == nil {
		t.Fatal("confirmation should dispatch the revalidated deletion")
	}
	msg := cmd()
	written, ok := msg.(assertionsWrittenMsg)
	if !ok || written.err != nil {
		t.Fatalf("write result = %#v", msg)
	}
	var req openfga.WriteAssertionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(req.Assertions) != 1 || req.Assertions[0].TupleKey.User != "user:b" {
		t.Fatalf("reordered confirmation deleted the wrong assertion: %+v", req.Assertions)
	}
}

func TestAssertionDeleteMissingIdentityDoesNothing(t *testing.T) {
	a := openfga.Assertion{TupleKey: openfga.CheckRequestTupleKey{User: "user:a", Relation: "viewer", Object: "doc:1"}}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL)
	m := newTestModel().(Model)
	m.client = cl
	m.section = secAssertions
	m.assertions = []openfga.Assertion{a}
	m.populateAssertions()
	nm, _ := m.handleSectionKey("d", key("d"))
	m = nm.(Model)
	m.assertions = nil
	m.populateAssertions()
	nm, cmd := m.handleKey(key("y"))
	got := nm.(Model)
	if cmd != nil {
		_ = cmd()
	}
	if calls.Load() != 0 || !strings.Contains(got.status, "nothing deleted") {
		t.Fatalf("missing target should not issue a write: calls=%d status=%q", calls.Load(), got.status)
	}
}

func TestUnrelatedLoadingDoesNotHideQueryResult(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secQuery
	m.loading = true
	m.pendingLoads = 1 // e.g. stores reload
	m.queryPendingGen = 0
	m.hasResult = true
	m.result = queryResultMsg{title: "completed result", lines: []string{"document:1"}}
	body := m.queryBody()
	if !strings.Contains(body, "completed result") || strings.Contains(body, "running…") {
		t.Fatalf("unrelated load hid the completed query result:\n%s", body)
	}
}

func TestAPILogUsesRecordedOriginAfterProfileSwitch(t *testing.T) {
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "https://server-a.example"})
	cfg.Set("prod", config.Profile{APIURL: "https://server-b.example"})
	a := cli.New(log.New(io.Discard), cfg, "test")
	rec := apilog.NewRecorder(10)
	rec.Add(apilog.Entry{Method: "GET", URL: "https://server-a.example/stores", Status: 200})
	m := apiLogModel()
	m.cli = a
	m.recorder = rec
	m.profile = "prod"
	m.apiURL = "https://server-b.example"
	m.refreshAPILogVP()
	body := m.apiLogsBody()
	if !strings.Contains(body, "server-a.example") || strings.Contains(body, "server-b.example") {
		t.Fatalf("history was relabeled with the current endpoint:\n%s", body)
	}
}

func TestMutationProgressIsSpecificAndDuplicateBlocked(t *testing.T) {
	m := newTestModel().(Model)
	m.storeCreating = true
	m.mutationStatus = "creating store demo…"
	if got := m.sectionStatus(); got != "creating store demo…" {
		t.Fatalf("section status = %q", got)
	}
	nm, cmd := m.enterForm(formCreateStore)
	if cmd != nil || nm.(Model).formKind == formCreateStore {
		t.Fatal("a second create form must not open while creation is in progress")
	}
}

func TestMutationOriginIncludesEffectiveProfileAndGeneration(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "model-1")
	m.profile = "effective"
	m.connGen = 9
	got := m.mutationOrigin(m.storeID, m.modelID)
	if got.connGen != 9 || got.profile != "effective" || got.storeID != "store-1" || got.modelID != "model-1" {
		t.Fatalf("origin = %+v", got)
	}
}

func TestStoreSwitchClearsResourcePendingState(t *testing.T) {
	m := newTestModel().(Model)
	m.storeID = "store-a"
	m.modelID = "model-a"
	m.modelApplying = true
	m.tupleMutating = true
	m.assertionsWriting = true
	m.queryPendingGen = 7
	m.mutationStatus = "writing tuple…"

	_ = m.selectStore(openfga.Store{ID: "store-b", Name: "B"})

	if m.modelApplying || m.tupleMutating || m.assertionsWriting || m.queryPendingGen != 0 || m.mutationStatus != "" {
		t.Fatalf("resource pending state survived store switch: %+v", m)
	}
}

func TestModelSwitchClearsResourcePendingState(t *testing.T) {
	m := newTestModel().(Model)
	m.storeID = "store-a"
	m.modelID = "model-a"
	m.pendingLoads = 1
	m.loading = true
	m.tupleMutating = true
	m.assertionsWriting = true
	m.assertions = []openfga.Assertion{{TupleKey: openfga.CheckRequestTupleKey{User: "user:old"}}}
	m.assertModelID = "model-a"
	m.queryPendingGen = 7
	m.hasResult = true
	m.mutationStatus = "writing assertions…"

	nm, _ := m.Update(modelLoadedMsg{storeID: "store-a", modelID: "model-b", graph: sampleGraph()})
	got := nm.(Model)
	if got.tupleMutating || got.assertionsWriting || got.queryPendingGen != 0 || got.mutationStatus != "" {
		t.Fatalf("resource pending state survived model switch: %+v", got)
	}
	if got.assertions != nil || got.assertModelID != "" || got.hasResult {
		t.Fatalf("model-scoped data survived model switch: %+v", got)
	}
}

func TestReselectionDropsOldMutationWithoutClearingNewerOne(t *testing.T) {
	m := newTestModel().(Model)
	m.storeID = "store-a"
	m.modelID = "model-a"
	m.tupleMutationGen = 1
	old := m.mutationOrigin(m.storeID, m.modelID, m.tupleMutationGen)

	_ = m.selectStore(openfga.Store{ID: "store-b", Name: "B"})
	_ = m.selectStore(openfga.Store{ID: "store-a", Name: "A"})
	m.tupleMutationGen++
	m.tupleMutating = true
	newGen := m.tupleMutationGen

	nm, cmd := m.Update(tupleWrittenMsg{origin: old, label: "old mutation"})
	got := nm.(Model)
	if cmd != nil {
		t.Fatal("old mutation completion triggered UI work")
	}
	if !got.tupleMutating || got.tupleMutationGen != newGen {
		t.Fatalf("old completion cleared newer mutation: pending=%v gen=%d want=%d",
			got.tupleMutating, got.tupleMutationGen, newGen)
	}
}
