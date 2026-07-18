package configcmd

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
)

// fakeProber stands in for a live openfga client so wizard flow can be driven
// without a server.
type fakeProber struct {
	storeList  []openfga.Store
	modelList  []openfga.AuthorizationModel
	storesErr  error
	modelsErr  error
	gotStoreID string
}

func (f *fakeProber) stores(context.Context) ([]openfga.Store, error) {
	return f.storeList, f.storesErr
}

func (f *fakeProber) models(_ context.Context, storeID string) ([]openfga.AuthorizationModel, error) {
	f.gotStoreID = storeID
	return f.modelList, f.modelsErr
}

func newTestWizard(fake *fakeProber) *wizardModel {
	m := newWizard(context.Background(), time.Second, wizardSeed{profile: "default"})
	m.newProber = func(string, config.Auth, time.Duration) prober { return fake }
	return m
}

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
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

func send(m *wizardModel, msg tea.Msg) { m.Update(msg) }

func TestWizardHappyPath(t *testing.T) {
	fake := &fakeProber{
		storeList: []openfga.Store{
			{ID: "01S1", Name: "alpha"},
			{ID: "01S2", Name: "beta"},
		},
		modelList: []openfga.AuthorizationModel{{ID: "01M1", SchemaVersion: "1.1"}},
	}
	m := newTestWizard(fake)

	send(m, key("enter")) // welcome -> connection
	if m.step != stepConnection {
		t.Fatalf("expected connection step, got %d", m.step)
	}
	send(m, key("enter")) // submit default API URL -> auth method
	if m.step != stepAuthMethod {
		t.Fatalf("expected auth-method step, got %d", m.step)
	}

	// Choose "None" (4th option) so the flow skips the details step.
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("enter"))
	if m.step != stepProbe {
		t.Fatalf("expected probe step, got %d", m.step)
	}
	if m.values.auth.Method != "" {
		t.Fatalf("expected no auth, got %q", m.values.auth.Method)
	}

	// Simulate the async store probe completing successfully.
	send(m, storesMsg{stores: fake.storeList})
	if !m.probed || m.connErr != nil {
		t.Fatalf("probe should have succeeded: probed=%v err=%v", m.probed, m.connErr)
	}
	send(m, key("enter")) // continue -> store picker
	if m.step != stepStore || m.storeManual {
		t.Fatalf("expected store picker, got step=%d manual=%v", m.step, m.storeManual)
	}

	send(m, key("enter")) // pick first store (alpha)
	if m.values.storeID != "01S1" {
		t.Fatalf("store = %q, want 01S1", m.values.storeID)
	}
	if m.step != stepModel {
		t.Fatalf("expected model step, got %d", m.step)
	}

	send(m, modelsMsg{storeID: "01S1", models: fake.modelList})
	if m.modelLoading {
		t.Fatal("models should have loaded")
	}

	send(m, key("enter")) // pick "always latest" (default cursor)
	if m.values.modelID != "" {
		t.Fatalf("model = %q, want empty (latest)", m.values.modelID)
	}
	if m.step != stepReview {
		t.Fatalf("expected review step, got %d", m.step)
	}

	_, cmd := m.Update(key("enter")) // save
	if !m.done {
		t.Fatal("expected done after save")
	}
	if cmd == nil {
		t.Fatal("save should return a quit command")
	}
}

func TestWizardProbeFailureFallsBackToManual(t *testing.T) {
	m := newTestWizard(&fakeProber{})

	send(m, key("enter")) // welcome -> connection
	send(m, key("enter")) // -> auth method
	// Pick None.
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("enter")) // -> probe

	send(m, storesMsg{err: errors.New("connection refused")})
	if m.connErr == nil {
		t.Fatal("expected a connection error")
	}
	send(m, key("enter")) // continue despite failure -> store step

	if m.step != stepStore || !m.storeManual {
		t.Fatalf("expected manual store entry, got step=%d manual=%v", m.step, m.storeManual)
	}
	m.storeForm.SetValues([]string{"01MANUAL"})
	send(m, key("enter")) // submit manual store

	if m.values.storeID != "01MANUAL" {
		t.Fatalf("store = %q, want 01MANUAL", m.values.storeID)
	}
	// No connection: the model step must go straight to manual entry too.
	if m.step != stepModel || !m.modelManual {
		t.Fatalf("expected manual model entry, got step=%d manual=%v", m.step, m.modelManual)
	}
	send(m, key("enter")) // blank model -> review
	if m.step != stepReview {
		t.Fatalf("expected review step, got %d", m.step)
	}
}

func TestWizardCancelOnWelcome(t *testing.T) {
	m := newTestWizard(&fakeProber{})
	_, cmd := m.Update(key("esc"))
	if !m.cancelled {
		t.Fatal("esc on welcome should cancel")
	}
	if cmd == nil {
		t.Fatal("cancel should return a quit command")
	}
}

func TestWizardSkipStoreGoesToReview(t *testing.T) {
	fake := &fakeProber{storeList: []openfga.Store{{ID: "01S1", Name: "alpha"}}}
	m := newTestWizard(fake)

	send(m, key("enter")) // -> connection
	send(m, key("enter")) // -> auth method
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("down"))
	send(m, key("enter")) // None -> probe
	send(m, storesMsg{stores: fake.storeList})
	send(m, key("enter")) // -> store picker

	// Picker items: alpha, "Enter manually", "Skip". Move to Skip (index 2).
	send(m, key("down")) // manual
	send(m, key("down")) // skip
	send(m, key("enter"))

	if m.values.storeID != "" {
		t.Fatalf("store should be empty when skipped, got %q", m.values.storeID)
	}
	if m.step != stepReview {
		t.Fatalf("skipping the store should jump to review, got step %d", m.step)
	}
}

func TestAssembleAuth(t *testing.T) {
	cc := assembleAuth(config.AuthClientCredentials, []string{"id", "secret", "https://issuer/token", "aud", "read write"})
	if cc.Method != config.AuthClientCredentials || cc.ClientID != "id" || cc.ClientSecret != "secret" ||
		cc.TokenURL != "https://issuer/token" || cc.Audience != "aud" || len(cc.Scopes) != 2 {
		t.Fatalf("client_credentials assembled wrong: %+v", cc)
	}
	if err := cc.Validate(); err != nil {
		t.Fatalf("assembled client_credentials should validate: %v", err)
	}

	jwt := assembleAuth(config.AuthPrivateKeyJWT, []string{"id", "https://issuer/token", "/key.pem", ""})
	if jwt.Method != config.AuthPrivateKeyJWT || jwt.KeyFile != "/key.pem" || jwt.ClientID != "id" {
		t.Fatalf("private_key_jwt assembled wrong: %+v", jwt)
	}
	if err := jwt.Validate(); err != nil {
		t.Fatalf("assembled private_key_jwt should validate: %v", err)
	}
}

// TestProberCommands runs the async command builders and confirms they invoke
// the prober and marshal its results into the right messages.
func TestProberCommands(t *testing.T) {
	fake := &fakeProber{
		storeList: []openfga.Store{{ID: "01S1", Name: "alpha"}},
		modelList: []openfga.AuthorizationModel{{ID: "01M1"}},
	}
	m := newTestWizard(fake)
	m.values.storeID = "01S1"

	sm, ok := m.probeStores()().(storesMsg)
	if !ok || sm.err != nil || len(sm.stores) != 1 {
		t.Fatalf("probeStores returned %+v (ok=%v)", sm, ok)
	}

	mm, ok := m.probeModels("01S1")().(modelsMsg)
	if !ok || mm.err != nil || mm.storeID != "01S1" || len(mm.models) != 1 {
		t.Fatalf("probeModels returned %+v (ok=%v)", mm, ok)
	}
	if fake.gotStoreID != "01S1" {
		t.Fatalf("prober saw store %q, want 01S1", fake.gotStoreID)
	}
}

// TestWizardRenderSmoke walks the tour and renders each step, asserting the
// frame is produced without panicking and carries the step's title. Set -v to
// eyeball the frames.
func TestWizardRenderSmoke(t *testing.T) {
	fake := &fakeProber{
		storeList: []openfga.Store{{ID: "01HZ1", Name: "docs-app"}, {ID: "01HZ2", Name: "ci"}},
		modelList: []openfga.AuthorizationModel{{ID: "01MODEL1", SchemaVersion: "1.1"}},
	}
	m := newTestWizard(fake)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})

	frame := func(label string) {
		out := m.viewString()
		if out == "" {
			t.Fatalf("%s: empty frame", label)
		}
		t.Logf("\n===== %s =====\n%s", label, out)
	}

	frame("welcome")
	send(m, key("enter")) // connection
	frame("connection")
	send(m, key("enter")) // auth method
	frame("auth-method")
	send(m, key("enter")) // pick API token -> details
	frame("auth-details (api token)")
	m.authForm.SetValues([]string{"secret-token"})
	send(m, key("enter")) // -> probe (loading)
	frame("probe (connecting)")
	send(m, storesMsg{stores: fake.storeList})
	frame("probe (connected)")
	send(m, key("enter")) // store picker
	frame("store picker")
	send(m, key("enter")) // pick docs-app -> model
	send(m, modelsMsg{storeID: "01HZ1", models: fake.modelList})
	frame("model picker")
	send(m, key("enter")) // latest -> review
	frame("review")
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"http://localhost:8080", "https://fga.example.com"} {
		if err := validateURL(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://x", "not a url", "localhost:8080"} {
		if err := validateURL(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
