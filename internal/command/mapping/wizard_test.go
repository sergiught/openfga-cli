package mapping

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
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

//nolint:unused // used by editor-screen tests added in task 12+
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
	if !strings.Contains(m.viewString(), "model.fga") && m.errMsg == "" {
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

	if !strings.Contains(m.viewString(), "a") || !strings.Contains(strings.ToLower(m.viewString()), "add") {
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
