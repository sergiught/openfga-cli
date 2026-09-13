package mapping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// atTrigger returns a wizard with one fresh rule, sitting on the trigger screen.
func atTrigger(t *testing.T) *wizardModel {
	t.Helper()
	m := atRulesHub(t)
	send(m, key("a"))
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
