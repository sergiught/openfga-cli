package playground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

// TestQueryFieldCount verifies list-relations uses two input fields (User,
// Object) while the other modes keep their three.
func TestQueryFieldCount(t *testing.T) {
	for mode, want := range map[string]int{
		"check":          3,
		"list-objects":   3,
		"list-users":     3,
		"list-relations": 2,
	} {
		if got := queryFieldCount(mode); got != want {
			t.Errorf("queryFieldCount(%q) = %d, want %d", mode, got, want)
		}
	}
}

// TestRelationsForType derives the candidate relations for list-relations from
// the object's type, returning them in the model's (sorted) order.
func TestRelationsForType(t *testing.T) {
	g := sampleGraph() // type document has relations owner, viewer

	got, err := relationsForType(g, "document:roadmap")
	if err != nil {
		t.Fatalf("relationsForType(document:roadmap) errored: %v", err)
	}
	if want := []string{"owner", "viewer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("relationsForType(document:roadmap) = %v, want %v", got, want)
	}
}

// TestRelationsForTypeErrors covers the inputs that cannot yield a relation set:
// a bare object with no type, an unknown type, and a type with no relations.
func TestRelationsForTypeErrors(t *testing.T) {
	g := sampleGraph()
	for _, object := range []string{"roadmap", "widget:x", "user:anne"} {
		if _, err := relationsForType(g, object); err == nil {
			t.Errorf("relationsForType(%q) = nil error, want error", object)
		}
	}
}

// TestQueryLabelsListRelations verifies the two-field labels for the new mode.
func TestQueryLabelsListRelations(t *testing.T) {
	labels, _ := queryLabels("list-relations")
	if labels[0] != "User" || labels[1] != "Object" {
		t.Errorf("queryLabels(list-relations) labels = %v, want [User Object …]", labels)
	}
}

// TestBuildQueryFormListRelationsHasTwoFields verifies the form carries two
// input fields plus the context toggle (three values), one fewer than check.
func TestBuildQueryFormListRelationsHasTwoFields(t *testing.T) {
	if n := len(buildQueryForm("list-relations", 80, false).Values()); n != 3 {
		t.Errorf("list-relations form has %d values, want 3 (User, Object, toggle)", n)
	}
	if n := len(buildQueryForm("check", 80, false).Values()); n != 4 {
		t.Errorf("check form has %d values, want 4 (User, Relation, Object, toggle)", n)
	}
}

// TestHistNotationListRelations verifies the Recent-strip shorthand for the new
// mode, which has a user and object but no single relation.
func TestHistNotationListRelations(t *testing.T) {
	got := histNotation(histEntry{mode: "list-relations", vals: [3]string{"user:anne", "document:roadmap", ""}})
	if want := "user:anne → document:roadmap"; got != want {
		t.Errorf("histNotation(list-relations) = %q, want %q", got, want)
	}
}

// TestListRelationsFormRunsAndRecordsHistory drives the query form end-to-end in
// list-relations mode: it derives the object type's relations from the model,
// tests them all via /batch-check, shows the allowed subset, and records the run
// in the Recent strip.
func TestListRelationsFormRunsAndRecordsHistory(t *testing.T) {
	var sentRelations []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/batch-check") {
			var body struct {
				Checks []struct {
					TupleKey struct {
						Relation string `json:"relation"`
					} `json:"tuple_key"`
					CorrelationID string `json:"correlation_id"`
				} `json:"checks"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			result := map[string]map[string]bool{}
			for _, c := range body.Checks {
				sentRelations = append(sentRelations, c.TupleKey.Relation)
				// Allow only viewer, so the result is a strict subset.
				result[c.CorrelationID] = map[string]bool{"allowed": c.TupleKey.Relation == "viewer"}
			}
			resp, _ := json.Marshal(map[string]any{"result": result})
			_, _ = w.Write(resp)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cl, _ := openfga.NewClient(srv.URL)
	a := cli.New(log.New(io.Discard), config.New(), "test")
	mdl := newModel(context.Background(), a, cl, "store-1", "")
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})
	m, _ = m.Update(modelLoadedMsg{modelID: "model-1", graph: sampleGraph()}) // document: owner, viewer

	m, _ = m.Update(key("6"))     // Query section (mode: check)
	m, _ = m.Update(key("enter")) // descend -> editing
	m, _ = m.Update(key("tab"))   // -> list-objects
	m, _ = m.Update(key("tab"))   // -> list-users
	m, _ = m.Update(key("tab"))   // -> list-relations
	if mode := queryModes[m.(Model).qmode]; mode != "list-relations" {
		t.Fatalf("expected list-relations mode after three tabs, got %q", mode)
	}

	for _, r := range "user:anne" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("enter")) // -> Object field
	for _, r := range "document:roadmap" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("enter")) // -> context toggle (last field)
	m = pump(t, m, key("enter")) // submit

	mod := m.(Model)
	if !mod.hasResult || mod.result.err != nil {
		t.Fatalf("expected a result; hasResult=%v err=%v", mod.hasResult, mod.result.err)
	}
	if want := []string{"owner", "viewer"}; !reflect.DeepEqual(sentRelations, want) {
		t.Errorf("batch-check tested relations %v, want %v (every relation on document)", sentRelations, want)
	}
	if want := []string{"viewer"}; !reflect.DeepEqual(mod.result.lines, want) {
		t.Errorf("result lines = %v, want %v (only the allowed relation)", mod.result.lines, want)
	}
	if mod.result.badge {
		t.Error("list-relations result should not carry an allow/deny badge")
	}
	if len(mod.history) != 1 || mod.history[0].mode != "list-relations" {
		t.Fatalf("list-relations query should be recorded once; history=%v", mod.history)
	}
	if strip := stripANSIView(mod.historyStrip(200)); !strings.Contains(strip, "user:anne → document:roadmap") {
		t.Errorf("recent strip should show the list-relations shorthand; got:\n%s", strip)
	}
}
