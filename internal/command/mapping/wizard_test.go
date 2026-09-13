package mapping

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

func send(m *wizardModel, msgs ...tea.Msg) {
	for _, msg := range msgs {
		m.Update(msg)
	}
}

func typeText(m *wizardModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func testModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		ID: "01J0",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{
				Type:      "organization",
				Relations: map[string]openfga.Userset{"member": {}, "admin": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
					"admin":  {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
				}},
			},
		},
	}
}

// newTestWizard starts a wizard sized to a wide terminal with a loader that
// succeeds. Pass nil to get a loader that fails.
func newTestWizard(t *testing.T, load modelLoader) *wizardModel {
	t.Helper()
	if load == nil {
		load = func(context.Context) (*openfga.AuthorizationModel, error) {
			return nil, errors.New("no server")
		}
	}
	m := newWizard(context.Background(), "mapping.yaml", "default", load)
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func TestWelcomeShowsTargetFileAndAdvancesOnEnter(t *testing.T) {
	m := newTestWizard(t, nil)
	if m.top() != screenWelcome {
		t.Fatalf("top = %v", m.top())
	}
	if !strings.Contains(m.viewString(), "mapping.yaml") {
		t.Fatalf("welcome does not name the file:\n%s", m.viewString())
	}
	send(m, key("enter"))
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want model source", m.top())
	}
}

func TestEscOnWelcomeCancels(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("esc"))
	if !m.cancelled {
		t.Fatal("expected cancellation")
	}
}

func TestModelSourceOffersConnectedStoreWhenAProfileIsActive(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("enter"))
	v := m.viewString()
	for _, want := range []string{"Connected store", "Model file", "Skip"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}

func TestModelSourceHidesConnectedStoreWithoutAProfile(t *testing.T) {
	m := newWizard(context.Background(), "mapping.yaml", "", nil)
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	send(m, key("enter"))
	if strings.Contains(m.viewString(), "Connected store") {
		t.Fatalf("connected store offered without a profile:\n%s", m.viewString())
	}
}

func TestSkipModelGoesStraightToTheRulesHub(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("enter")) // welcome -> model source
	// Skip is the last row and the recommended default: the cursor starts there.
	send(m, key("enter"))
	if m.top() != screenRules {
		t.Fatalf("top = %v, want rules", m.top())
	}
	if !m.index.Empty() {
		t.Fatal("skipping must leave the model index empty")
	}
}

func TestLoadFromServerIndexesTheModel(t *testing.T) {
	loaded := false
	m := newTestWizard(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		loaded = true
		return testModel(), nil
	})
	send(m, key("enter"))         // welcome -> model source
	send(m, key("up"), key("up")) // move to "Connected store"
	send(m, key("enter"))         // choose it: dispatches the load

	// The load runs as a command; drive it directly, the way bubbletea would.
	cmd := m.loadCmd
	if cmd == nil {
		t.Fatal("expected a load command")
	}
	m.Update(cmd())

	if !loaded {
		t.Fatal("loader was not called")
	}
	if m.index.Empty() {
		t.Fatal("model was not indexed")
	}
	if got := m.index.RelationsFor("organization"); len(got) != 2 {
		t.Fatalf("relations = %v", got)
	}
	if m.top() != screenRules {
		t.Fatalf("top = %v, want rules", m.top())
	}
}

func TestServerLoadFailureContinuesWithoutAModel(t *testing.T) {
	m := newTestWizard(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		return nil, errors.New("connection refused")
	})
	send(m, key("enter"), key("up"), key("up"), key("enter"))
	m.Update(m.loadCmd())

	if m.top() != screenRules {
		t.Fatalf("a failed load must not block: top = %v", m.top())
	}
	if !m.index.Empty() {
		t.Fatal("index should be empty after a failure")
	}
	v := m.viewString()
	if !strings.Contains(v, "connection refused") {
		t.Fatalf("the error should be shown:\n%s", v)
	}
	if !strings.Contains(v, "free text") {
		t.Fatalf("the reassurance should be shown:\n%s", v)
	}
}

// atInFlightLoad walks to the model source, picks the connected store and
// stops with the fetch dispatched but not yet delivered — the state a user is
// in while an unreachable server is being retried.
func atInFlightLoad(t *testing.T, load modelLoader) *wizardModel {
	t.Helper()
	m := newTestWizard(t, load)
	send(m, key("enter"), key("up"), key("up"), key("enter"))
	if !m.loading {
		t.Fatal("choosing the connected store must put the wizard in its loading state")
	}
	return m
}

func TestAnInFlightLoadSaysSoAndOffersAWayOut(t *testing.T) {
	m := atInFlightLoad(t, nil)
	v := m.viewString()
	if !strings.Contains(v, "Reading the authorization model") {
		t.Fatalf("a load in flight must say so:\n%s", v)
	}
	if !strings.Contains(v, "cancel") {
		t.Fatalf("a load in flight must offer a way out:\n%s", v)
	}
}

func TestASecondEnterDuringALoadDoesNotStartAnother(t *testing.T) {
	calls := 0
	m := atInFlightLoad(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		calls++
		return testModel(), nil
	})

	// Pressing enter again is what a user does when nothing appears to happen.
	// Each extra fetch would push the rules hub again on its way back.
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Fatal("a second enter dispatched another fetch")
	}
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want the source screen to stay put", m.top())
	}

	m.Update(m.loadCmd())
	if calls != 1 {
		t.Fatalf("loader called %d times, want 1", calls)
	}
	if m.top() != screenRules {
		t.Fatalf("top = %v, want rules", m.top())
	}
	if m.loading {
		t.Fatal("loading should be over once the result arrives")
	}
}

func TestEscCancelsALoadAndIgnoresItsLateResult(t *testing.T) {
	m := atInFlightLoad(t, nil)
	cmd := m.loadCmd

	send(m, key("esc"))
	if m.loading {
		t.Fatal("esc must stop the wait")
	}
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want the source picker back", m.top())
	}
	if !strings.Contains(m.viewString(), "Connected store") {
		t.Fatalf("the picker should be back:\n%s", m.viewString())
	}

	// The abandoned fetch still reports back. Acting on it would drop the user
	// into the rules hub they just backed out of.
	m.Update(cmd())
	if m.top() != screenModelSource {
		t.Fatalf("a cancelled load moved the wizard to %v", m.top())
	}
}

func TestModelFileLoadsAndBadFileStaysOnTheField(t *testing.T) {
	dir := t.TempDir()
	good := dir + "/model.fga"
	if err := os.WriteFile(good, []byte("model\n  schema 1.1\ntype user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := dir + "/broken.fga"
	if err := os.WriteFile(bad, []byte("this is not a model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestWizard(t, nil)
	send(m, key("enter"), key("up"), key("enter")) // model source -> "Model file"
	if m.top() != screenModelFile {
		t.Fatalf("top = %v", m.top())
	}

	m.modelPath.SetValues([]string{bad})
	send(m, key("enter"))
	if m.top() != screenModelFile {
		t.Fatalf("a bad model must keep the field: top = %v", m.top())
	}
	if m.errMsg == "" {
		t.Fatalf("expected an error on the field:\n%s", m.viewString())
	}

	m.modelPath.SetValues([]string{good})
	send(m, key("enter"))
	if m.top() != screenRules {
		t.Fatalf("a good model should advance: top = %v", m.top())
	}
	if m.index.Empty() {
		t.Fatal("model was not indexed")
	}
}

func TestRulesHubEmptyStateAndQuit(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("enter"), key("enter")) // skip the model

	if !strings.Contains(strings.ToLower(m.viewString()), "add") {
		t.Fatalf("empty state should explain `a`:\n%s", m.viewString())
	}
	// With no rules there is nothing to lose, so esc quits without confirming.
	send(m, key("esc"))
	if !m.cancelled {
		t.Fatal("expected cancellation from an empty hub")
	}
}

func TestPushPopStack(t *testing.T) {
	m := newTestWizard(t, nil)
	m.push(screenRules)
	m.push(screenRule)
	if m.top() != screenRule {
		t.Fatalf("top = %v", m.top())
	}
	m.pop()
	if m.top() != screenRules {
		t.Fatalf("top = %v", m.top())
	}
	// Popping past the root must not panic or empty the stack.
	for i := 0; i < 5; i++ {
		m.pop()
	}
	if m.top() != screenWelcome {
		t.Fatalf("top = %v, want welcome", m.top())
	}
}

// hasTerminalControls reports whether s carries a sequence that does something
// to the terminal rather than just colouring it. Lipgloss paints with SGR
// (`ESC [ … m`), which is why a blanket search for ESC would be useless here.
func hasTerminalControls(s string) bool {
	return strings.Contains(s, "\x1b[2J") || strings.Contains(s, "\x1b]") || strings.Contains(s, "\a")
}

// Sample events are pasted in or read off disk — a payload captured from a
// webhook or a log — so every value the wizard echoes back is attacker-shaped
// text. Screen-clearing CSI and OSC window-title sequences have to be stripped
// at each render boundary, the same way the rest of the CLI does it.
func TestEventContentCannotDriveTheTerminal(t *testing.T) {
	const attack = "\x1b[2J\x1b[1;1H\x1b]0;pwned\a"

	m := atRulesHub(t)
	m.doc.Rules = []mapping.Rule{{
		Name: attack + "rule",
		When: "true",
		Sample: &mapping.Sample{Label: attack + "event", Event: map[string]any{
			"type": attack + "event",
			"data": map[string]any{"evil": attack + "id"},
		}},
		Tuples: []mapping.Tuple{{
			User:     "user:1",
			Relation: "member",
			Object:   "organization:{{ input.data.evil }}",
		}},
	}}
	m.syncRules()

	// The path picker shows example values straight out of the event.
	items := m.pathItems()
	if len(items) == 0 {
		t.Fatal("expected the sample to yield paths")
	}
	for _, it := range items {
		if hasTerminalControls(it.DescText) {
			t.Fatalf("path row %q carries terminal controls: %q", it.TitleText, it.DescText)
		}
	}

	// The rules hub shows the rule name and the sample's label, both auto-filled
	// from the event's own `type`.
	if v := m.rules.View(); hasTerminalControls(v) {
		t.Fatalf("the rules hub carries terminal controls:\n%q", v)
	}

	// The preview pane shows the tuples the event evaluated to.
	if len(m.preview.Tuples) == 0 {
		t.Fatalf("expected an evaluated tuple, diagnostics = %+v", m.preview.Diagnostics)
	}
	if v := m.evaluationLines(m.contentWidth()); hasTerminalControls(v) {
		t.Fatalf("the preview pane carries terminal controls:\n%q", v)
	}
}

// TestSanitizingADiagnosticKeepsItsLayout guards the seam between stripping
// escapes and preserving shape: mapper points a caret at the offending column
// on its own line, and style.SanitizeTerminal deletes newlines along with the
// escapes, so sanitizing a diagnostic whole collapses the caret into nonsense.
func TestSanitizingADiagnosticKeepsItsLayout(t *testing.T) {
	const msg = "unexpected token EOF (1:13)\n | input.type ==\x1b[31m\n | ............^"

	got := sanitizeKeepingLines(msg)
	if hasTerminalControls(got) {
		t.Fatalf("escapes survived: %q", got)
	}
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Fatalf("the caret art lost its lines: %q", got)
	}
	if !strings.HasSuffix(got, "| ............^") {
		t.Fatalf("the caret should still end the message: %q", got)
	}
}

func TestPreviewPaneStacksOnNarrowTerminals(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("enter"), key("enter"))

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.sideBySide() {
		t.Fatal("120 columns should be side by side")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	if m.sideBySide() {
		t.Fatal("80 columns should stack")
	}
	// Neither layout may panic or render nothing.
	if m.viewString() == "" {
		t.Fatal("empty view")
	}
}

// ctrl+c is the one key a user reaches for when they want out, and on most of
// these screens the focus is inside a text field where esc means "done", not
// "quit". bubbletea delivers ctrl+c as an ordinary key, so every screen that
// does not handle it traps the user.
func TestCtrlCQuitsFromAnyScreen(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) *wizardModel
	}{
		{"welcome", func(t *testing.T) *wizardModel { return newTestWizard(t, nil) }},
		{"model source", func(t *testing.T) *wizardModel {
			m := newTestWizard(t, nil)
			send(m, key("enter"))
			return m
		}},
		{"rules hub", atRulesHub},
		{"trigger form", func(t *testing.T) *wizardModel {
			m := atRulesHub(t)
			send(m, key("a"))
			return m
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := c.open(t)
			send(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if !m.cancelled {
				t.Fatalf("ctrl+c did not cancel on %s (top = %v)", c.name, m.top())
			}
		})
	}
}

// A form submits on enter at its last field and then stops accepting keys, so a
// screen that does not act on the submit goes inert and silently swallows
// everything typed next.
func TestEnterOnTheLastFieldCommitsAndLeavesTheForm(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if m.top() != screenTrigger {
		t.Fatalf("top = %v, want trigger", m.top())
	}

	typeText(m, "myrule")
	send(m, key("tab"))
	typeText(m, "true")
	send(m, key("enter"))

	if m.top() == screenTrigger {
		t.Fatal("the form is still on screen after submitting")
	}
	if len(m.doc.Rules) != 1 {
		t.Fatalf("rules = %d", len(m.doc.Rules))
	}
	if got := m.doc.Rules[0]; got.Name != "myrule" || got.When != "true" {
		t.Fatalf("rule = %+v", got)
	}
}
