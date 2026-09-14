package mapping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// atTrigger returns a wizard with one fresh rule, sitting on the trigger
// screen. "a" now opens the payload-kind fork rather than a blank rule (see
// TestForkOwnPayloadPasteAppendsARuleOnAccept), so this wires the rule
// directly — exactly what addRule used to do — to keep testing the trigger
// screen itself independent of that fork.
func atTrigger(t *testing.T) *wizardModel {
	t.Helper()
	m := atRulesHub(t)
	addRuleAtTrigger(m)
	if m.top() != screenTrigger {
		t.Fatalf("top = %v", m.top())
	}
	return m
}

func TestEventPickListsTheCatalogAndTheTwoEscapes(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if m.top() != screenEventPick {
		t.Fatalf("top = %v", m.top())
	}
	v := m.viewString()
	for _, want := range []string{"organization.member.added", "Paste JSON", "Load from file"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}

func TestPickingACatalogEventSetsTheSampleAndAutoFills(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))

	r := m.rule()
	if r.Sample == nil {
		t.Fatal("no sample set")
	}
	if r.Sample.Label != "organization.member.added" {
		t.Fatalf("label = %q", r.Sample.Label)
	}
	if r.Sample.Event["type"] != "organization.member.added" {
		t.Fatalf("event = %+v", r.Sample.Event)
	}
	if r.Name != "organization.member.added" {
		t.Fatalf("name = %q", r.Name)
	}
	if r.When != `input.type == "organization.member.added"` {
		t.Fatalf("when = %q", r.When)
	}
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want a return to the trigger", m.top())
	}
}

// TestForkCatalogPickAppendsARuleOnAFreshDocument guards the fix for the
// catalog branch of the add-rule fork: "a" -> kind screen -> Auth0 -> pick
// must create the rule on accept, exactly like the paste branch already did.
// Before the fix, m.rule() returned nil on a fresh document and the sample
// was silently dropped.
func TestForkCatalogPickAppendsARuleOnAFreshDocument(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if m.top() != screenEventPick {
		t.Fatalf("top = %v", m.top())
	}
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.doc.Rules))
	}
	if m.ruleIdx != 0 {
		t.Fatalf("ruleIdx = %d, want it to address the new rule", m.ruleIdx)
	}
	r := m.rule()
	if r == nil || r.Sample == nil || r.Sample.Label != "organization.member.added" {
		t.Fatalf("rule = %+v, want the picked sample attached", r)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule's hub", m.top())
	}
}

// TestForkCatalogPickLeavesAnExistingRuleAlone guards the other half of the
// same fix: reached from the fork with a rule already in the document, before
// the fix m.ruleIdx was a stale pointer and the pick clobbered that rule's
// Sample, AutoName and AutoWhen instead of appending a new one.
func TestForkCatalogPickLeavesAnExistingRuleAlone(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = mappingRule("existing", "user.created", 0)
	m.syncRules()
	existing := m.doc.Rules[0]

	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))

	if len(m.doc.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(m.doc.Rules))
	}
	if m.doc.Rules[0].Name != existing.Name || m.doc.Rules[0].Sample != existing.Sample || m.doc.Rules[0].AutoName != existing.AutoName {
		t.Fatalf("the existing rule was touched: %+v", m.doc.Rules[0])
	}
}

// TestForkCatalogEscAppendsNothing checks that abandoning a fork pick strands
// no rule behind it. The document starts with a rule already in it so "rules
// unchanged" is a real claim, not the zero value a fresh document would give
// for free.
func TestForkCatalogEscAppendsNothing(t *testing.T) {
	m := atRulesHub(t)
	m.doc.Rules = mappingRule("existing", "user.created", 0)
	m.syncRules()

	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if m.top() != screenEventPick {
		t.Fatalf("top = %v", m.top())
	}
	send(m, key("esc"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.doc.Rules))
	}
}

// TestCtrlEFromTriggerPicksWithoutAppendingARule is the pre-existing
// ctrl+e-from-trigger path: it must keep landing back on the trigger form
// with the sample on the rule that was already there, and never append.
func TestCtrlEFromTriggerPicksWithoutAppendingARule(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1 (ctrl+e must not append)", len(m.doc.Rules))
	}
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want the trigger form", m.top())
	}
	r := m.rule()
	if r == nil || r.Sample == nil || r.Sample.Label != "organization.member.added" {
		t.Fatalf("rule = %+v, want the picked sample attached", r)
	}
}

// TestForkPasteJSONAppendsARuleAndDoesNotHang guards the round-2 fix: fork
// detection by stack position (rather than presence) misclassified this path
// — event pick sits between payloadKind and paste — as non-fork, sending
// acceptPick's pop loop hunting for a screenTrigger that was never pushed. A
// hang here means the test itself times out rather than failing an assertion.
func TestForkPasteJSONAppendsARuleAndDoesNotHang(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if !m.events.SelectID(pasteID) {
		t.Fatal("could not select paste")
	}
	send(m, key("enter"))
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v", m.top())
	}

	m.paste.SetValue(`{"type": "user.created"}`)
	send(m, key("ctrl+d"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.doc.Rules))
	}
	r := m.rule()
	if r == nil || r.Sample == nil || r.Sample.Label != "user.created" {
		t.Fatalf("rule = %+v, want the pasted sample attached", r)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule's hub", m.top())
	}
}

// TestForkLoadFromFileAppendsARuleOnAFreshDocument covers the third
// accept-handler, keyEventFile, now routed through acceptPick like its
// siblings. Before this fix it always assumed a rule already existed.
func TestForkLoadFromFileAppendsARuleOnAFreshDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	if err := os.WriteFile(path, []byte(`{"type":"from.file","data":{"object":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := atRulesHub(t)
	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if !m.events.SelectID(fileID) {
		t.Fatal("could not select load from file")
	}
	send(m, key("enter"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}

	m.eventPath.SetValues([]string{path})
	send(m, key("enter"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.doc.Rules))
	}
	r := m.rule()
	if r == nil || r.Sample == nil || r.Sample.Label != "from.file" {
		t.Fatalf("rule = %+v, want the loaded sample attached", r)
	}
	if m.top() != screenRule {
		t.Fatalf("top = %v, want the new rule's hub", m.top())
	}
}

// TestForkLoadFromFileLeavesAnExistingRuleAlone is keyEventFile's half of the
// round-1 clobbering defect: reached from the fork with a rule already in the
// document, it must append a new rule rather than overwriting that one.
func TestForkLoadFromFileLeavesAnExistingRuleAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	if err := os.WriteFile(path, []byte(`{"type":"from.file","data":{"object":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := atRulesHub(t)
	m.doc.Rules = mappingRule("existing", "user.created", 0)
	m.syncRules()
	existing := m.doc.Rules[0]

	send(m, key("a"), key("enter")) // add rule: kind screen -> Auth0 -> catalog
	if !m.events.SelectID(fileID) {
		t.Fatal("could not select load from file")
	}
	send(m, key("enter"))

	m.eventPath.SetValues([]string{path})
	send(m, key("enter"))

	if len(m.doc.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(m.doc.Rules))
	}
	if m.doc.Rules[0].Name != existing.Name || m.doc.Rules[0].Sample != existing.Sample || m.doc.Rules[0].AutoName != existing.AutoName {
		t.Fatalf("the existing rule was touched: %+v", m.doc.Rules[0])
	}
}

// TestCtrlEPasteJSONAppendsNothing is the pre-existing ctrl+e -> catalog ->
// "Paste JSON" path: it must keep landing back on the trigger form and never
// append.
func TestCtrlEPasteJSONAppendsNothing(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if !m.events.SelectID(pasteID) {
		t.Fatal("could not select paste")
	}
	send(m, key("enter"))
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v", m.top())
	}

	m.paste.SetValue(`{"type": "user.created"}`)
	send(m, key("ctrl+d"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1 (ctrl+e must not append)", len(m.doc.Rules))
	}
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want the trigger form", m.top())
	}
}

// TestCtrlELoadFromFileAppendsNothing is the pre-existing ctrl+e -> catalog ->
// "Load from file" path: it must keep landing back on the trigger form and
// never append.
func TestCtrlELoadFromFileAppendsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	if err := os.WriteFile(path, []byte(`{"type":"from.file","data":{"object":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if !m.events.SelectID(fileID) {
		t.Fatal("could not select load from file")
	}
	send(m, key("enter"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}

	m.eventPath.SetValues([]string{path})
	send(m, key("enter"))

	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d, want 1 (ctrl+e must not append)", len(m.doc.Rules))
	}
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want the trigger form", m.top())
	}
}

func TestPastedJSONBecomesTheSample(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	if !m.events.SelectID("__paste__") {
		t.Fatal("could not select paste")
	}
	send(m, key("enter"))
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v", m.top())
	}

	m.paste.SetValue(`{"type":"custom.thing","data":{"object":{"id":"abc"}}}`)
	send(m, key("ctrl+d")) // ctrl+d accepts, since enter inserts a newline

	r := m.rule()
	if r.Sample == nil || r.Sample.Event["type"] != "custom.thing" {
		t.Fatalf("sample = %+v", r.Sample)
	}
	if r.Sample.Label != "custom.thing" {
		t.Fatalf("label = %q", r.Sample.Label)
	}
	if r.When != `input.type == "custom.thing"` {
		t.Fatalf("when = %q", r.When)
	}
}

func TestPastedJSONWithoutATypeStillWorks(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	m.events.SelectID("__paste__")
	send(m, key("enter"))

	m.paste.SetValue(`{"data":{"object":{"id":"abc"}}}`)
	send(m, key("ctrl+d"))

	r := m.rule()
	if r.Sample == nil {
		t.Fatal("no sample")
	}
	if r.Sample.Label != "pasted event" {
		t.Fatalf("label = %q", r.Sample.Label)
	}
	// With no type there is nothing to auto-fill `when` from, so it stays empty.
	if r.When != "" {
		t.Fatalf("when = %q, want empty", r.When)
	}
}

func TestBadPastedJSONStaysOnTheScreen(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	m.events.SelectID("__paste__")
	send(m, key("enter"))

	m.paste.SetValue(`{"type": oops}`)
	send(m, key("ctrl+d"))

	if m.top() != screenEventPaste {
		t.Fatalf("top = %v, want to stay on paste", m.top())
	}
	if m.errMsg == "" {
		t.Fatal("expected an error message")
	}
	if r := m.rule(); r.Sample != nil {
		t.Fatalf("a bad paste must not set a sample: %+v", r.Sample)
	}
}

func TestLoadEventFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	if err := os.WriteFile(path, []byte(`{"type":"from.file","data":{"object":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := atTrigger(t)
	send(m, key("ctrl+e"))
	m.events.SelectID("__file__")
	send(m, key("enter"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}

	m.eventPath.SetValues([]string{path})
	send(m, key("enter"))

	r := m.rule()
	if r.Sample == nil || r.Sample.Event["type"] != "from.file" {
		t.Fatalf("sample = %+v", r.Sample)
	}
	if r.Sample.Label != "from.file" {
		t.Fatalf("label = %q", r.Sample.Label)
	}
}

func TestMissingEventFileStaysOnTheField(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	m.events.SelectID("__file__")
	send(m, key("enter"))

	m.eventPath.SetValues([]string{filepath.Join(t.TempDir(), "nope.json")})
	send(m, key("enter"))

	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}
	if m.errMsg == "" {
		t.Fatal("expected an error")
	}
}

func TestChoosingASampleRefreshesThePreview(t *testing.T) {
	m := atTrigger(t)
	// Give the rule a tuple so the sample has something to produce.
	m.rule().Tuples = mappingTuple()
	send(m, key("ctrl+e"))
	m.events.SelectID("organization.member.added")
	send(m, key("enter"))

	if len(m.preview.Tuples) != 1 {
		t.Fatalf("preview tuples = %+v", m.preview.Tuples)
	}
	if m.preview.Tuples[0].Object != "organization:org_1234567890abcdef" {
		t.Fatalf("tuple = %+v", m.preview.Tuples[0])
	}
}

// mappingTuple is the member-added tuple, used where a rule needs to emit
// something for the preview to show.
func mappingTuple() []mapping.Tuple {
	return []mapping.Tuple{{
		User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
		Relation: "member",
		Object:   "organization:{{ input.data.object.organization.id }}",
	}}
}
