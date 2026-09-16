package mapping

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// ansiSeq matches the colour and style escapes lipgloss writes. Assertions
// about wording read better against the text alone: a phrase that happens to
// straddle two styles is still one phrase to the person reading the screen.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(view string) string { return ansiSeq.ReplaceAllString(view, "") }

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
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

// previewOf renders the preview pane at the size paneView would give it, so a
// test asserting on the pane alone sees the same rows and columns the composed
// view does.
func previewOf(m *wizardModel) string {
	return m.previewPane(m.previewWidth(), m.height-statusRows-frameRows)
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

// Every other command resolves its glyphs through icons.I(), so `icons = off`
// or a Nerd Font rung reaches all of them. The wizard used to be the one
// exception, hardcoding the unicode glyphs whatever the config said.
func TestTheWizardFollowsTheIconRung(t *testing.T) {
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })

	icons.Apply(icons.ModeUnicode)
	m := newTestWizard(t, nil)
	unicodeStore := icons.I().Store
	if out := plain(m.viewString()); !strings.Contains(out, unicodeStore) {
		t.Fatalf("the unicode rung's store glyph is missing:\n%s", out)
	}

	icons.Apply(icons.ModeOff)
	if out := plain(m.viewString()); strings.Contains(out, unicodeStore) {
		t.Fatalf("glyphs are off and the wizard still draws one:\n%s", out)
	}
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
		t.Fatalf("top = %v, want the model source", m.top())
	}
}

// The model question comes first and the event pick is underneath it, so that
// the types and relations a tuple may name are known before the user is asked
// to name any. An earlier cut asked the payload question first, on the reasoning
// that a model has no context yet — but the pickers it feeds are the context.
//
// Skipping is one key, and what it reveals is the event pick, not the welcome
// screen: the model is optional, and declining it must not cost the user the
// step they came to do.
func TestTheModelIsAskedBeforeTheEvent(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("enter"))
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want the model source first", m.top())
	}
	send(m, key("esc"))
	if m.top() != screenPayloadKind {
		t.Fatalf("skipping the model left the user on %v, not the payload kind", m.top())
	}
}

// The other two ways of answering have to arrive at the same screen, or the
// user who actually loads a model is worse off than the one who skipped. A
// fetch returns through modelLoadedMsg, which is the path Skip does not take.
func TestALoadedModelLandsOnTheEventFork(t *testing.T) {
	m := newTestWizard(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		return testModel(), nil
	})
	send(m, key("enter"))
	selectSource(t, m, "server")
	send(m, key("enter"))
	if !m.loading {
		t.Fatal("selecting the connected store did not start a fetch")
	}
	m.Update(modelLoadedMsg{model: testModel(), gen: m.loadGen})

	if m.top() != screenPayloadKind {
		t.Fatalf("a loaded model left the user on %v, not the payload kind", m.top())
	}
	if m.index.Empty() {
		t.Fatal("the model was not indexed")
	}
}

func TestEscOnWelcomeCancels(t *testing.T) {
	m := newTestWizard(t, nil)
	send(m, key("esc"))
	if !m.cancelled {
		t.Fatal("expected cancellation")
	}
}

// atModelSource returns a wizard sitting on the source picker, the way a user
// reaches it with `m` from the hub. Set the stack rather than typing: this
// helper means "a wizard sitting on the source picker", not "whatever the
// welcome screen happens to lead to this month".
func atModelSource(t *testing.T, load modelLoader) *wizardModel {
	t.Helper()
	m := newTestWizard(t, load)
	m.stack = []screen{screenRules, screenModelSource}
	return m
}

// selectSource moves the source picker to the row with this value, so a test
// says which source it means instead of counting rows from a default.
func selectSource(t *testing.T, m *wizardModel, value string) {
	t.Helper()
	for i := 0; i < m.sourcePick.Len(); i++ {
		m.sourcePick.SetCursor(i)
		if m.sourcePick.Selected().Value == value {
			return
		}
	}
	t.Fatalf("the %q source is not offered", value)
}

// atModelFile reaches the typed path field, which sits one key behind the
// browser the file source now opens.
func atModelFile(t *testing.T, m *wizardModel) {
	t.Helper()
	selectSource(t, m, "file")
	send(m, key("enter"))
	if m.top() != screenModelBrowse {
		t.Fatalf("choosing the file source should open the browser: top = %v", m.top())
	}
	send(m, key("ctrl+p"))
	if m.top() != screenModelFile {
		t.Fatalf("ctrl+p should open the path field: top = %v", m.top())
	}
}

// Finding #5: once past the model source there was no key back to it, and Skip
// was pre-selected, so two enters left the user in a modelless wizard for good.
func TestTheHubCanReturnToTheModelSource(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("m"))
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want the model source", m.top())
	}
}

// The `m` binding above is only discoverable if the hub's footer advertises
// it — this guards the hint half of that pair. A model is indexed so the
// status chip reads "N types" rather than "no model", leaving the footer's
// own "model" label as the only match for the substring below.
func TestTheHubAdvertisesTheModelSourceKey(t *testing.T) {
	m := atRulesHub(t)
	m.index = mapping.IndexModel(testModel())
	addRuleAtTrigger(m)
	send(m, key("esc"), key("esc")) // trigger -> the rule -> the rules hub, non-empty
	if m.top() != screenRules {
		t.Fatalf("top = %v, want the rules hub", m.top())
	}
	if v := m.viewString(); !strings.Contains(v, "model") {
		t.Fatalf("the hub should hint at `m` for the model source:\n%s", v)
	}
}

func TestTheRecipeScreenCanReturnToTheModelSource(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	selectEvent(t, m, "organization.member.added")
	send(m, key("m"))
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want the model source", m.top())
	}
}

// A model chosen mid-flow used to push the hub on top of the fork instead of
// popping back to it, stranding screenPayloadKind on the stack. fromFork then
// read every later ctrl+e as an add-rule fork and appended a duplicate rule
// instead of editing the one the user had open.
func TestChoosingAModelMidFlowUnwindsTheFork(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter")) // -> recipe
	send(m, key("m"))     // -> model source
	selectSource(t, m, "skip")
	send(m, key("enter")) // choose a source; must come back, not push
	send(m, key("enter")) // use the recipe, which is what it must have come back to

	addRuleAtTrigger(m)
	before := len(m.doc.Rules)
	send(m, key("ctrl+e"))
	if !m.events.SelectID("organization.created") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))
	if m.top() == screenRecipe {
		t.Fatal("ctrl+e was misread as an add-rule fork")
	}
	if len(m.doc.Rules) != before {
		t.Fatalf("ctrl+e appended a rule: rules = %d, want %d", len(m.doc.Rules), before)
	}
}

// The store-path twin of TestChoosingAModelMidFlowUnwindsTheFork: the same
// stranding bug lived in the modelLoadedMsg handler, reached only once a
// dispatched fetch actually resolves, which the "skip" path above never
// exercises.
func TestChoosingAModelViaTheStoreMidFlowUnwindsTheFork(t *testing.T) {
	m := newTestWizard(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		return testModel(), nil
	})
	m.stack = []screen{screenRules}
	send(m, key("a"), key("enter"))
	if !m.events.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter")) // -> recipe
	send(m, key("m"))     // -> model source
	selectSource(t, m, "server")
	send(m, key("enter")) // choose it: dispatches the load

	cmd := m.loadCmd
	if cmd == nil {
		t.Fatal("expected a load command")
	}
	m.Update(cmd()) // deliver the result; must pop back, not push
	if m.top() != screenRecipe {
		t.Fatalf("top = %v, want back on the recipe screen", m.top())
	}
	send(m, key("enter")) // use the recipe, which is what it must have come back to

	addRuleAtTrigger(m)
	before := len(m.doc.Rules)
	send(m, key("ctrl+e"))
	if !m.events.SelectID("organization.created") {
		t.Fatal("could not select the event")
	}
	send(m, key("enter"))
	if m.top() == screenRecipe {
		t.Fatal("ctrl+e was misread as an add-rule fork")
	}
	if len(m.doc.Rules) != before {
		t.Fatalf("ctrl+e appended a rule: rules = %d, want %d", len(m.doc.Rules), before)
	}
}

// A failed load says two things at once: what went wrong, and that it does not
// have to stop you. The second only makes sense beside the first — on its own
// "That's OK" is reassurance about nothing, and it used to outlive the error by
// the whole rest of the session, on every screen the user visited.
func TestTheReassuranceDoesNotOutliveTheErrorItReassuresAbout(t *testing.T) {
	m := atModelSource(t, nil) // the default test loader fails
	selectSource(t, m, "server")
	send(m, key("enter"))
	m.Update(m.loadCmd())

	if v := plain(m.viewString()); !strings.Contains(v, "free text") {
		t.Fatalf("the failed load did not offer the fallback at all:\n%s", v)
	}

	send(m, key("down"))
	if v := plain(m.viewString()); strings.Contains(v, "free text") {
		t.Fatalf("the note outlived the error beside it:\n%s", v)
	}
}

func TestModelSourceOffersConnectedStoreWhenAProfileIsActive(t *testing.T) {
	m := atModelSource(t, nil)
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
	// Built by hand rather than through atModelSource, which is the only way to
	// get a wizard with no profile, so the stack is set the same way it does.
	m.stack = []screen{screenRules, screenModelSource}
	if strings.Contains(m.viewString(), "Connected store") {
		t.Fatalf("connected store offered without a profile:\n%s", m.viewString())
	}
}

func TestSkipModelGoesStraightToTheRulesHub(t *testing.T) {
	m := atModelSource(t, nil)
	selectSource(t, m, "skip")
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
	m := atModelSource(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		loaded = true
		return testModel(), nil
	})
	selectSource(t, m, "server")
	send(m, key("enter")) // choose it: dispatches the load

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
	m := atModelSource(t, func(context.Context) (*openfga.AuthorizationModel, error) {
		return nil, errors.New("connection refused")
	})
	selectSource(t, m, "server")
	send(m, key("enter"))
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

// atInFlightLoad picks the connected store and stops with the fetch dispatched
// but not yet delivered — the state a user is in while an unreachable server is
// being retried.
func atInFlightLoad(t *testing.T, load modelLoader) *wizardModel {
	t.Helper()
	m := atModelSource(t, load)
	selectSource(t, m, "server")
	send(m, key("enter"))
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

	m := atModelSource(t, nil)
	atModelFile(t, m)

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

// ctrl+s used to reach the field package on this screen, where it means
// "submit this form" — it left the screen up with the form completed, and
// Init() kept that completed flag set forever, permanently deadening the field
// even after re-entry. The wizard now intercepts ctrl+s before the field ever
// sees it, so the old route in is gone; what has to keep working is that
// pressing it here still leaves a half-typed path editable. Resume() clears
// the flag, and this is the test that would notice if it stopped.
func TestCtrlSOnModelFileDoesNotPermanentlyDeadenTheField(t *testing.T) {
	m := atModelSource(t, nil)
	atModelFile(t, m)

	typeText(m, "/tmp/a.json")
	send(m, key("ctrl+s"))
	send(m, key("esc"))
	send(m, key("esc"))
	if m.top() != screenModelSource {
		t.Fatalf("top = %v, want back on the source picker", m.top())
	}

	// Re-enter the screen the way a user fixing a typo would.
	atModelFile(t, m)
	before := m.modelPath.Values()[0]
	typeText(m, "X")
	if after := m.modelPath.Values()[0]; after == before {
		t.Fatalf("typing after re-entry did not reach the field: value stayed %q", before)
	}
}

// The Event file screen's twin of TestCtrlSOnModelFileDoesNotPermanentlyDeadenTheField.
func TestCtrlSOnEventFileDoesNotPermanentlyDeadenTheField(t *testing.T) {
	m := atTrigger(t)
	send(m, key("ctrl+e"))
	m.events.SelectID(fileID)
	send(m, key("enter"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}

	typeText(m, "/tmp/a.json")
	// A rule exists by the time this screen is reachable, so ctrl+s has
	// something to save and opens the dialog over the half-typed path. Backing
	// out of it has to return here, not discard the screen.
	send(m, key("ctrl+s"))
	if m.top() != screenConfirmSave {
		t.Fatalf("ctrl+s = %v, want the save dialog", m.top())
	}
	send(m, key("esc"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v, want back on the file screen", m.top())
	}
	send(m, key("esc"))
	if m.top() != screenEventPick {
		t.Fatalf("top = %v, want back on the event pick", m.top())
	}

	// Re-enter the screen the way a user fixing a typo would.
	m.events.SelectID(fileID)
	send(m, key("enter"))
	if m.top() != screenEventFile {
		t.Fatalf("top = %v", m.top())
	}
	before := m.eventPath.Values()[0]
	typeText(m, "X")
	if after := m.eventPath.Values()[0]; after == before {
		t.Fatalf("typing after re-entry did not reach the field: value stayed %q", before)
	}
}

func TestRulesHubEmptyStateAndQuit(t *testing.T) {
	m := atRulesHub(t)

	// The sentence the empty state alone writes. "add" on its own is in the
	// footer's key hints on this screen too, so it would pass with no empty
	// state rendered at all.
	if !strings.Contains(m.viewString(), "No rules yet.") {
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

// Every screen wears the same frame — that is the whole point of the visual
// pass — and the editors keep their preview inside it.
func TestEveryScreenIsFramed(t *testing.T) {
	m := atRulesHub(t)
	out := m.viewString()
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Fatalf("the hub is not framed:\n%s", out)
	}
}

func TestAFramedEditorStillShowsItsPreview(t *testing.T) {
	m := atTuples(t)
	send(m, key("a"))
	typeText(m, "organization:acme")
	out := m.viewString()
	if !strings.Contains(out, "╭") {
		t.Fatalf("the tuple form is not framed:\n%s", out)
	}
	// Assert against the preview pane rather than the whole view: the form field
	// the user just typed into echoes the same text, so a whole-view grep cannot
	// tell "the preview shows it" from "the input shows it".
	if pane := previewOf(m); !strings.Contains(pane, "organization:acme") {
		t.Fatalf("the preview vanished from the framed editor:\n%s", pane)
	}
}

// layoutSizes spreads over the shapes the wizard changes behaviour at: the
// declared floor, either side of the side-by-side threshold, and short
// terminals at each width, because height is the dimension a centred card and
// a padded pane overrun in different ways.
var layoutSizes = []struct{ w, h int }{
	{minCols, minRows},         // the floor
	{minCols, 30},              // narrowest, with room to spare
	{minCols, 40},              // narrow and tall, so a form shows every field at once
	{minCols + 2, minRows + 1}, // just off the floor in both dimensions
	{72, minRows},              // stacked, short
	{72, 24},                   // stacked, mid width
	{sideBySideMin - 1, 20},    // the last stacked width
	{sideBySideMin, minRows},   // side by side at the height floor
	{sideBySideMin, 30},        // side by side
	{120, 18},                  // wide and short
	{200, 60},                  // large
}

// doesNotFit names the first way a rendered screen fails to fit a w×h
// terminal, or returns "" when it fits. All three checks are the same bug seen
// from different sides: content the layout never bounded. A missing closing
// border is its own check because a body trimmed to the terminal's height
// after being framed loses the frame's last row rather than the content's.
func doesNotFit(out string, w, h int) string {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if lw := lipgloss.Width(line); lw > w {
			return fmt.Sprintf("a line is %d cells wide, wider than the %d the terminal has", lw, w)
		}
	}
	if len(lines) > h {
		return fmt.Sprintf("the screen is %d rows tall, taller than the %d the terminal has", len(lines), h)
	}
	if !strings.Contains(out, "╰") {
		return "the frame has no closing border"
	}
	return ""
}

// No screen may render past the terminal it was given, in either dimension:
// the frame's border and padding are new cells competing for the same space,
// and nothing else in the suite checks for this class of overflow.
func TestNoScreenOverflowsTheTerminal(t *testing.T) {
	for scr, c := range screenChrome {
		for _, sz := range layoutSizes {
			for _, loaded := range []bool{false, true} {
				m := newTestWizard(t, nil)
				m.stack = []screen{scr}
				if loaded {
					// The three bodies whose length the wizard does not choose: an
					// OS error carrying a path, a note it writes itself, and the
					// condition parameters a model happens to declare. Every screen
					// gets them, because errMsg and noteMsg outlive the screen that
					// set them.
					m.errMsg = "could not read /home/j/projects/acme/testdata/events/organization.member.added.json: no such file or directory"
					m.noteMsg = "That's OK — pickers will accept free text."
					m.ctxKeys = []string{"region", "tenant_id", "requested_at", "correlation_id"}
				}
				m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
				out := m.viewString()
				if why := doesNotFit(out, sz.w, sz.h); why != "" {
					// Errorf, not Fatalf: this sweep is how a layout regression's
					// shape gets read, and the shape is which sizes broke, not the
					// first one. Fatalf reports a single combo out of hundreds.
					t.Errorf("%q at %dx%d (loaded=%v): %s:\n%s", c.title, sz.w, sz.h, loaded, why, out)
				}
			}
		}
	}

	// The generic pass above never exercises screenRecipe's own content: driven
	// by stack alone it sees the zero auth0.Recipe{}, whose Maps() is false and
	// Requires is empty, so recipeMappingBlock and recipeModelBlock both render
	// nothing. Run the same invariant again per catalog entry, with the state
	// openRecipe actually sets, so the screen that motivated this test is the
	// one it checks. The model is deliberately left unloaded: with none loaded,
	// recipeModelBlock takes the !statuses[0].Checked branch and renders the DSL
	// for every requirement, the widest output the screen can produce.
	for _, e := range auth0.Catalog() {
		for _, sz := range layoutSizes {
			m := newTestWizard(t, nil)
			m.stack = []screen{screenRecipe}
			m.recipeEvent = e
			m.recipe = e.Recipe
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			out := m.viewString()
			if why := doesNotFit(out, sz.w, sz.h); why != "" {
				t.Errorf("%s at %dx%d: %s:\n%s", e.Type, sz.w, sz.h, why, out)
			}
		}
	}
}

// The wordmark is the one piece of the welcome card that carries no
// information, so it is what a short terminal loses — but only a short one.
func TestTheWelcomeWordmarkYieldsToAShortTerminal(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want bool
	}{
		{120, 40, true},
		{minCols, minRows, false},
	} {
		m := newTestWizard(t, nil)
		m.Update(tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
		if m.top() != screenWelcome {
			t.Fatalf("top = %v, want the welcome screen", m.top())
		}
		// The block art is the only place in the wizard that draws a full block.
		if got := strings.Contains(m.viewString(), "█"); got != tc.want {
			t.Fatalf("at %dx%d the wordmark is shown = %v, want %v:\n%s",
				tc.w, tc.h, got, tc.want, m.viewString())
		}
	}
}

func TestPreviewPaneStacksOnNarrowTerminals(t *testing.T) {
	m := atRulesHub(t)

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
			return atModelSource(t, nil)
		}},
		{"payload kind", func(t *testing.T) *wizardModel {
			m := atRulesHub(t)
			send(m, key("a"))
			if m.top() != screenPayloadKind {
				t.Fatalf("top = %v, want the payload-kind screen", m.top())
			}
			return m
		}},
		{"rules hub", atRulesHub},
		{"trigger form", func(t *testing.T) *wizardModel {
			m := atRulesHub(t)
			addRuleAtTrigger(m)
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
	addRuleAtTrigger(m)
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

// textWidgetCase names one screen with a text-entry widget: how to reach it,
// the screen it must land on, and how to read the value back. Shared by
// TestTypingReachesEveryTextWidget and TestPasteReachesEveryTextWidget so both
// drive the same eight screens rather than keeping two tables in sync by hand.
type textWidgetCase struct {
	name  string
	reach func(t *testing.T) *wizardModel
	want  screen
	value func(m *wizardModel) string
	// assertDocument, when set, checks that a paste into this screen's widget
	// also reached the document through the screen's live-commit tail (see
	// routePaste). Filled in for the five multi-field rule forms (trigger,
	// tuple, variable, iterator, filter); nil for the three single-field
	// screens, which have nothing else to commit to.
	assertDocument func(t *testing.T, m *wizardModel)
}

func textWidgetCases() []textWidgetCase {
	return []textWidgetCase{
		{
			name:  "trigger",
			reach: atTrigger,
			want:  screenTrigger,
			value: func(m *wizardModel) string { return m.trigger.Values()[0] },
			assertDocument: func(t *testing.T, m *wizardModel) {
				t.Helper()
				if got := m.rule().Name; got != "pasted" {
					t.Fatalf("paste reached the widget but not the document: rule name = %q", got)
				}
			},
		},
		{
			name: "model file",
			reach: func(t *testing.T) *wizardModel {
				m := atModelSource(t, nil)
				atModelFile(t, m)
				return m
			},
			want:  screenModelFile,
			value: func(m *wizardModel) string { return m.modelPath.Values()[0] },
		},
		{
			name: "event file",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("ctrl+e"))
				m.events.SelectID(fileID)
				send(m, key("enter"))
				return m
			},
			want:  screenEventFile,
			value: func(m *wizardModel) string { return m.eventPath.Values()[0] },
		},
		{
			name: "event paste",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("ctrl+e"))
				m.events.SelectID(pasteID)
				send(m, key("enter"))
				return m
			},
			want:  screenEventPaste,
			value: func(m *wizardModel) string { return m.paste.Value() },
		},
		{
			name: "tuple",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("esc"))                                                                    // trigger -> the rule hub
				send(m, key("down"), key("down"), key("down"), key("down"), key("down"), key("enter")) // Tuples
				send(m, key("a"))
				return m
			},
			want:  screenTuple,
			value: func(m *wizardModel) string { return m.tupleForm.Values()[0] },
			assertDocument: func(t *testing.T, m *wizardModel) {
				t.Helper()
				ts := m.tuples()
				if ts == nil || len(*ts) == 0 || (*ts)[0].Object != "pasted" {
					t.Fatalf("paste reached the widget but not the document: tuples = %+v", ts)
				}
			},
		},
		{
			name: "variable",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("esc"))                             // trigger -> the rule hub
				send(m, key("down"), key("down"), key("enter")) // Variables
				send(m, key("a"))
				return m
			},
			want:  screenVariable,
			value: func(m *wizardModel) string { return m.varForm.Values()[0] },
			assertDocument: func(t *testing.T, m *wizardModel) {
				t.Helper()
				if got := m.rule().Variables[m.varIdx].Name; got != "pasted" {
					t.Fatalf("paste reached the widget but not the document: variable name = %q", got)
				}
			},
		},
		{
			name: "iterator",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("esc"))                                          // trigger -> the rule hub
				send(m, key("down"), key("down"), key("down"), key("enter")) // Iterator hub
				send(m, key("enter"))                                        // its Source row
				return m
			},
			want:  screenIterForm,
			value: func(m *wizardModel) string { return m.iterForm.Values()[0] },
			// This is the blank-source guard's own test, not the shared "before
			// != after" check above: that paste (into a blank field) already makes
			// the source non-blank, which the guard would have let through anyway.
			// Detecting the guard's removal needs a paste that finds the field
			// blank while the rule already carries an iterator worth protecting.
			assertDocument: func(t *testing.T, m *wizardModel) {
				t.Helper()
				r := m.rule()
				r.Iterator = &mapping.Iterator{
					Source: "input.data.object.identities",
					As:     "identity",
					Tuples: []mapping.Tuple{{Object: "connection:1", Relation: "member", User: "user:1"}},
				}
				m.iterForm.SetValues([]string{"", "identity"}) // as if mid-retype
				m.Update(tea.PasteMsg{Content: ""})
				if m.rule().Iterator == nil {
					t.Fatal("a transient blank source dropped the iterator: the blank-source guard was not applied")
				}
			},
		},
		{
			name: "tuple filter",
			reach: func(t *testing.T) *wizardModel {
				m := atTrigger(t)
				send(m, key("esc"))                                                       // trigger -> the rule hub
				send(m, key("down"), key("down"), key("down"), key("down"), key("enter")) // Tuple filters
				send(m, key("a"))
				return m
			},
			want:  screenFilter,
			value: func(m *wizardModel) string { return m.filterForm.Values()[0] },
			assertDocument: func(t *testing.T, m *wizardModel) {
				t.Helper()
				if got := m.rule().Filters[m.filterIdx].User; got != "pasted" {
					t.Fatalf("paste reached the widget but not the document: filter user = %q", got)
				}
			},
		},
	}
}

// TestTypingReachesEveryTextWidget guards the focus bug found in task 8b: a
// screen whose widget is built but never focused discards every keystroke, and
// a test that injects the value with SetValue/SetValues instead of typing it
// never notices. Every screen listed here hosts a text-entry widget, so every
// row must type and see the value change — no shortcuts.
func TestTypingReachesEveryTextWidget(t *testing.T) {
	for _, c := range textWidgetCases() {
		t.Run(c.name, func(t *testing.T) {
			m := c.reach(t)
			if m.top() != c.want {
				t.Fatalf("top = %v, want %v", m.top(), c.want)
			}
			before := c.value(m)
			typeText(m, "Z")
			after := c.value(m)
			if after == before {
				t.Fatalf("typing did not reach the widget: value stayed %q", before)
			}
		})
	}
}

// TestPasteReachesEveryTextWidget guards task 8b's second finding: a real
// terminal paste (tea.PasteMsg) must reach the same eight widgets typing does.
// For the five multi-field rule forms it also asserts the paste reached the
// document, not only the widget: that is what each form's live-commit tail
// (mirrored in routePaste) buys.
func TestPasteReachesEveryTextWidget(t *testing.T) {
	for _, c := range textWidgetCases() {
		t.Run(c.name, func(t *testing.T) {
			m := c.reach(t)
			if m.top() != c.want {
				t.Fatalf("top = %v, want %v", m.top(), c.want)
			}
			before := c.value(m)
			m.Update(tea.PasteMsg{Content: "pasted"})
			after := c.value(m)
			if after == before {
				t.Fatalf("paste did not reach the widget: value stayed %q", before)
			}
			if c.assertDocument != nil {
				c.assertDocument(t, m)
			}
		})
	}
}
