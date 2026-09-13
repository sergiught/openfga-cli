package mapping

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// completeRule gives the wizard one valid, sampled rule.
func completeRule(m *wizardModel) {
	m.doc.Rules = []mapping.Rule{{
		Name:   "organization.member.added",
		When:   `input.type == "organization.member.added"`,
		Sample: &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()},
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "member",
			Object:   "organization:{{ input.data.object.organization.id }}",
		}},
	}}
	m.syncRules()
}

func TestSaveAValidDocumentWritesAndQuits(t *testing.T) {
	m := atRulesHub(t)
	completeRule(m)

	send(m, key("ctrl+s"))
	if m.top() != screenConfirmSave {
		t.Fatalf("top = %v", m.top())
	}
	// A clean document offers Save first and says so.
	if !strings.Contains(m.viewString(), "2 rules") && !strings.Contains(m.viewString(), "1 rule") {
		t.Fatalf("the dialog should summarise:\n%s", m.viewString())
	}
	send(m, key("enter"))

	if !m.done {
		t.Fatal("expected the wizard to finish")
	}
	if m.result == nil {
		t.Fatal("no result")
	}
	if m.result.rules != 1 || m.result.tuples != 1 {
		t.Fatalf("result = %+v", m.result)
	}
	if m.result.tests != 1 {
		t.Fatalf("tests = %d, want 1 generated from the sample", m.result.tests)
	}
	if !strings.Contains(string(m.result.data), "organization.member.added") {
		t.Fatalf("data = %s", m.result.data)
	}
}

func TestSavedYAMLParsesAndCarriesTests(t *testing.T) {
	m := atRulesHub(t)
	completeRule(m)
	send(m, key("ctrl+s"), key("enter"))

	var got struct {
		Version string           `yaml:"version"`
		Rules   []map[string]any `yaml:"rules"`
		Tests   []map[string]any `yaml:"tests"`
	}
	if err := yaml.Unmarshal(m.result.data, &got); err != nil {
		t.Fatalf("the saved file does not parse: %v\n%s", err, m.result.data)
	}
	if got.Version != "1" {
		t.Fatalf("version = %q", got.Version)
	}
	if len(got.Rules) != 1 || len(got.Tests) != 1 {
		t.Fatalf("rules = %d, tests = %d", len(got.Rules), len(got.Tests))
	}
	if strings.Contains(string(m.result.data), "sample") {
		t.Fatalf("wizard-only state leaked into the file:\n%s", m.result.data)
	}
}

// Lint never parses an expression, so it has nothing to say about an unfinished
// `when` — which the user is one keystroke away from at all times. Only mapper
// knows whether the document compiles, and the dialog claims exactly that, so
// the gate has to ask it.
func TestSaveGateSpeaksForMapperNotOnlyLint(t *testing.T) {
	m := atRulesHub(t)
	completeRule(m)
	m.doc.Rules[0].When = "input.type =="
	m.syncRules()

	if got := mapping.Blocking(m.problems); len(got) != 0 {
		t.Fatalf("lint is supposed to be blind here, but reported %+v", got)
	}
	if len(m.preview.Diagnostics) == 0 {
		t.Fatal("mapper is supposed to reject this document")
	}

	send(m, key("ctrl+s"))
	if m.top() != screenConfirmSave {
		t.Fatalf("top = %v", m.top())
	}

	summary := m.saveSummary()
	if strings.Contains(summary, "The mapping compiles.") {
		t.Fatalf("the dialog claims a document mapper rejects compiles:\n%s", summary)
	}
	if !strings.Contains(strings.ToLower(summary), "save anyway") {
		t.Fatalf("the dialog should offer to save anyway:\n%s", summary)
	}
	// The diagnostic has to be in the list, not merely counted.
	firstLine, _, _ := strings.Cut(m.preview.Diagnostics[0].Message, "\n")
	if !strings.Contains(summary, firstLine) {
		t.Fatalf("mapper's diagnostic %q is missing from the dialog:\n%s", firstLine, summary)
	}

	send(m, key("enter"))
	if m.done || m.result != nil {
		t.Fatalf("enter must not write a document mapper rejects: done=%v result=%+v", m.done, m.result)
	}

	// `s` stays the deliberate escape hatch.
	send(m, key("s"))
	if !m.done || m.result == nil {
		t.Fatalf("s should still save anyway: done=%v result=%+v", m.done, m.result)
	}
}

// The dialog reads m.preview, which is recomputed on mutation rather than when
// the dialog opens. Breaking the trigger on the way to it and reading the
// dialog is the check that the preview it reads is the current one.
func TestSaveDialogSeesAnEditMadeOnTheWayToIt(t *testing.T) {
	m := atRulesHub(t)
	completeRule(m)

	send(m, key("enter")) // rules hub -> rule hub
	send(m, key("enter")) // Trigger is the first row
	if m.top() != screenTrigger {
		t.Fatalf("top = %v", m.top())
	}

	const when = 1
	m.trigger.FocusIndex(when)
	m.trigger.SetCursor(when, len(m.trigger.Values()[when]))
	typeText(m, " ==") // break the expression

	send(m, key("esc"))    // commit the trigger, back to the rule hub
	send(m, key("ctrl+s")) // straight to the dialog

	if summary := m.saveSummary(); strings.Contains(summary, "The mapping compiles.") {
		t.Fatalf("the dialog is reading a preview from before the edit:\n%s", summary)
	}
}

func TestSaveWithProblemsAsksFirst(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m) // a rule with no tuples
	send(m, key("esc"), key("esc"))
	send(m, key("ctrl+s"))

	v := m.viewString()
	if !strings.Contains(v, "error") {
		t.Fatalf("the dialog should report the problems:\n%s", v)
	}
	if !strings.Contains(strings.ToLower(v), "save anyway") {
		t.Fatalf("the dialog should offer to save anyway:\n%s", v)
	}

	// Back returns to the hub without writing.
	send(m, key("esc"))
	if m.done {
		t.Fatal("esc must not save")
	}
	if m.top() != screenRules {
		t.Fatalf("top = %v", m.top())
	}
}

func TestSaveAnywayWritesTheBrokenDocument(t *testing.T) {
	m := atRulesHub(t)
	addRuleAtTrigger(m) // a rule with no tuples
	send(m, key("esc"), key("esc"))
	send(m, key("ctrl+s"))
	send(m, key("s")) // save anyway

	if !m.done || m.result == nil {
		t.Fatalf("done = %v result = %+v", m.done, m.result)
	}
	if m.result.tests != 0 {
		t.Fatalf("a document that does not compile can carry no tests: %d", m.result.tests)
	}
}

func TestSaveCountsTuplesAcrossRulesAndIterators(t *testing.T) {
	m := atRulesHub(t)
	completeRule(m)
	m.doc.Rules[0].Iterator = &mapping.Iterator{
		Source: "input.data.object.identities",
		As:     "identity",
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "member", Object: "connection:1"}},
	}
	m.syncRules()
	send(m, key("ctrl+s"), key("enter"))

	if m.result.tuples != 2 {
		t.Fatalf("tuples = %d, want 2", m.result.tuples)
	}
}

func TestEmptyDocumentCannotBeSaved(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("ctrl+s"))
	// With no rules there is nothing to write; the dialog says so and Save is
	// not offered.
	if !strings.Contains(m.viewString(), "no rules") {
		t.Fatalf("view = %s", m.viewString())
	}
	send(m, key("enter"))
	if m.done {
		t.Fatal("an empty document must not be saved")
	}
}

// TestTheWholeFlowWritesAMappingFile drives the wizard end to end the way a
// user would — welcome, skip the model, add a rule, pick its sample event,
// fill in one tuple, save — and reads the file back off disk. This stands in
// for the brief's Step 6 manual walkthrough, which cannot run here: runInit's
// wizardEligible check requires a real terminal on both ends of the pipe, so
// this test drives the model directly and writes to a t.TempDir() path
// instead of `/tmp`.
func TestTheWholeFlowWritesAMappingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	m := newWizard(context.Background(), path, "", nil)
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	send(m, key("enter"), key("enter")) // welcome -> model source -> skip

	send(m, key("a"), key("down"), key("enter")) // add rule: kind screen -> own payload -> paste
	m.paste.SetValue(`{"type": "organization.member.added", "data": {"object": {"organization": {"id": "org_1234567890abcdef"}, "user": {"user_id": "auth0|507f1f77bcf86cd799439020"}}}}`)
	send(m, key("ctrl+d")) // accept the pasted event, lands on the rule hub

	send(m, key("down"), key("down"), key("down"), key("down"), key("down")) // Trigger -> Tuples
	send(m, key("enter"))                                                    // open Tuples
	send(m, key("a"))                                                        // add a tuple, opens the form on Object
	typeText(m, "organization:{{ input.data.object.organization.id }}")
	send(m, key("tab"))
	typeText(m, "member")
	send(m, key("tab"))
	typeText(m, "user:{{ fga_escape(input.data.object.user.user_id) }}")
	send(m, key("esc")) // commit the tuple, back to the tuple list
	send(m, key("esc")) // back to the rule hub

	send(m, key("esc")) // rule hub -> rules hub
	send(m, key("ctrl+s"), key("enter"))

	if !m.done || m.result == nil {
		t.Fatalf("done = %v result = %+v", m.done, m.result)
	}
	if err := saveMapping(m.path, m.result.data); err != nil {
		t.Fatalf("saveMapping: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read back %s: %v", path, err)
	}
	data := string(written)

	if !strings.Contains(data, `version: "1"`) {
		t.Fatalf("missing version: %s", data)
	}
	if m.result.rules != 1 {
		t.Fatalf("rules = %d, want 1", m.result.rules)
	}
	if m.result.tuples != 1 {
		t.Fatalf("tuples = %d, want 1", m.result.tuples)
	}
	if m.result.tests != 1 {
		t.Fatalf("tests = %d, want 1", m.result.tests)
	}
	if strings.Contains(data, "sample:") {
		t.Fatalf("wizard-only sample leaked into the file:\n%s", data)
	}
}
