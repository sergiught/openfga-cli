package mapping

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping"
)

// exampleDir is the mapping the docs guide walks through. These tests keep the
// example consistent with its model and events, and pin the counts the guide
// quotes. That auth0.yaml still matches what the recipes produce is checked in
// the tui package (TestExampleMappingIsWhatTheRecipesProduce).
const exampleDir = "../../../examples/mappings"

func exampleEvents(t *testing.T) (*mapping.File, []map[string]any) {
	t.Helper()
	f, err := mapping.Load(exampleDir + "/auth0.yaml")
	if err != nil {
		t.Fatalf("load auth0.yaml: %v", err)
	}
	data, err := os.ReadFile(exampleDir + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapping.ParseEvents(bytes.NewReader(data), "events.jsonl")
	if err != nil {
		t.Fatalf("parse events.jsonl: %v", err)
	}
	return f, events
}

func TestExampleMappingValidatesAgainstItsModel(t *testing.T) {
	configtest.Isolate(t)
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"),
		"validate", exampleDir+"/auth0.yaml", "--model-file", exampleDir+"/model.fga")
	if err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if !strings.Contains(out, "4 rules, 4 tests; checked against ../../../examples/mappings/model.fga (4 types)") {
		t.Fatalf("the guide quotes a different summary: %s", out)
	}
}

func TestExampleMappingTestsPass(t *testing.T) {
	configtest.Isolate(t)
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"),
		"test", exampleDir+"/auth0.yaml")
	if err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if !strings.Contains(out, "4 passed, 0 failed") {
		t.Fatalf("want every test to pass: %s", out)
	}
}

func TestExampleEventsEachChangeSomething(t *testing.T) {
	configtest.Isolate(t)
	f, events := exampleEvents(t)
	if len(events) != 5 {
		t.Fatalf("the guide says 5 events, got %d", len(events))
	}
	for i, ev := range events {
		p := mapping.Evaluate(context.Background(), f.Doc, ev)
		if len(p.Tuples)+len(p.Filters) == 0 {
			t.Errorf("event %d changes nothing", i+1)
		}
	}
}

func TestExampleEventsWriteAndDeleteAsTheGuideSays(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil,
		exampleDir+"/auth0.yaml", "--events", exampleDir+"/events.jsonl", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if want := "5 events: 5 writes, 3 deletes, 0 matched no rule"; !strings.Contains(stderr, want) {
		t.Fatalf("the guide says %q, got:\n%s", want, stderr)
	}
}
