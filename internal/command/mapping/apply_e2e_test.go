package mapping

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func allowed(t *testing.T, cl *openfga.Client, user, relation, object string) bool {
	t.Helper()
	resp, err := cl.Relationships.Check(context.Background(), &openfga.CheckRequest{
		TupleKey: openfga.CheckRequestTupleKey{User: user, Relation: relation, Object: object},
	})
	if err != nil {
		t.Fatalf("check %s %s %s: %v", user, relation, object, err)
	}
	return resp.Allowed
}

// TestApplyEndToEnd drives apply against a real embedded OpenFGA server: a dry
// run writes nothing, a forced run writes the tuple, and a rerun is safe.
func TestApplyEndToEnd(t *testing.T) {
	configtest.Isolate(t)
	ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, storeID, modelID, stop, err := modeltest.Seed(context.Background(), ws, "documents/owner-is-viewer", modeltest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: endpoint, StoreID: storeID, ModelID: modelID})
	newCLI := func() *cli.CLI { return cli.New(log.New(io.Discard), cfg, "test") }

	dir := t.TempDir()
	m := writeFile(t, dir, "mapping.yaml", `version: "1"
rules:
  - name: shared
    when: input.type == "folder.shared"
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "folder:{{ input.folder }}"
`)
	ev := writeFile(t, dir, "events.jsonl", "{\"type\":\"folder.shared\",\"user\":\"zoe\",\"folder\":\"f1\"}\n{\"type\":\"other\"}\n")

	if _, err := run(t, newCLI(), "apply", m, "--events", ev, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	cl, err := openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID))
	if err != nil {
		t.Fatal(err)
	}
	if allowed(t, cl, "user:zoe", "viewer", "folder:f1") {
		t.Fatal("dry run wrote")
	}
	if out, err := run(t, newCLI(), "apply", m, "--events", ev, "--force"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !allowed(t, cl, "user:zoe", "viewer", "folder:f1") {
		t.Fatal("apply did not write")
	}
	// The rerun sends the same plan; on_duplicate ignore makes it a no-op.
	out, err := run(t, newCLI(), "apply", m, "--events", ev, "--force")
	if err != nil || !strings.Contains(out, "sent 1 write and 0 deletes") {
		t.Fatalf("rerun: err=%v out=%s", err, out)
	}
}

// storeTuples lists every tuple in the store, sorted, with its condition.
func storeTuples(t *testing.T, cl *openfga.Client) []string {
	t.Helper()
	var out []string
	for tp, err := range cl.Tuples.ReadAll(context.Background(), &openfga.ReadRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		s := tp.Key.User + " " + tp.Key.Relation + " " + tp.Key.Object
		if c := tp.Key.Condition; c != nil {
			s += fmt.Sprintf(" %s%v", c.Name, c.Context)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestApplyEndToEndWithAConditionalPatchRule drives a rule with a patch
// tuple_filter and a condition against a real server: apply writes the tuple,
// a rerun has nothing to change, and a stored copy with a numeric context
// (mapper renders strings) is replaced in two requests.
func TestApplyEndToEndWithAConditionalPatchRule(t *testing.T) {
	configtest.Isolate(t)
	ws, err := modeltest.LoadWorkspace("testdata/conditional")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, storeID, modelID, stop, err := modeltest.Seed(context.Background(), ws, "seed", modeltest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: endpoint, StoreID: storeID, ModelID: modelID})
	newCLI := func() *cli.CLI { return cli.New(log.New(io.Discard), cfg, "test") }
	cl, err := openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	m := writeFile(t, dir, "mapping.yaml", `version: "1"
rules:
  - name: grant
    when: input.type == "grant"
    tuple_filters:
      - relation: member
        object: "team:{{ input.team }}"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
        condition: lvl
        context:
          level: "{{ input.level }}"
`)
	ev := writeFile(t, dir, "events.jsonl", `{"type":"grant","user":"a","team":"t1","level":2}`+"\n")
	want := []string{"user:a member team:t1 lvlmap[level:2]", "user:z member team:t9"}

	if out, err := run(t, newCLI(), "apply", m, "--events", ev, "--force"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if got := storeTuples(t, cl); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("store = %q", got)
	}
	if out, err := run(t, newCLI(), "apply", m, "--events", ev, "--force"); err != nil || !strings.Contains(out, "nothing to change") {
		t.Fatalf("rerun: err=%v\n%s", err, out)
	}

	// Another writer stores the same tuple with a numeric context.
	key := openfga.NewTupleKey("user:a", "member", "team:t1")
	if err := cl.Tuples.Write(context.Background(), &openfga.WriteRequest{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}); err != nil {
		t.Fatal(err)
	}
	key.Condition = &openfga.RelationshipCondition{Name: "lvl", Context: map[string]any{"level": 2}}
	if err := cl.Tuples.Write(context.Background(), &openfga.WriteRequest{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, newCLI(), "apply", m, "--events", ev, "--force")
	if err != nil || !strings.Contains(out, "event 1 · grant (split into 2 requests)") ||
		!strings.Contains(out, "- user:a member team:t1 (lvl: level=2)") || !strings.Contains(out, `+ user:a member team:t1 (lvl: level="2")`) {
		t.Fatalf("numeric context: err=%v\n%s", err, out)
	}
	got := storeTuples(t, cl)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("store = %q", got)
	}
	// The stored context is mapper's string again, not the number.
	for tp, err := range cl.Tuples.ReadAll(context.Background(), &openfga.ReadRequest{TupleKey: &openfga.ReadRequestTupleKey{User: "user:a", Relation: "member", Object: "team:t1"}}) {
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := tp.Key.Condition.Context["level"].(string); !ok || v != "2" {
			t.Fatalf("context = %#v", tp.Key.Condition.Context)
		}
	}
}
