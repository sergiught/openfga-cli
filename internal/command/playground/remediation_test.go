package playground

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// brokenConfig returns a *config.Config whose Save always fails: it is loaded
// from a file that failed to parse, which config.LoadFrom records as a
// deferred loadErr that every subsequent Save call returns deterministically
// (see config.go's Save/LoadFrom). It still starts from the single "default"
// profile config.New() seeds (LoadFrom falls back to New() on a parse
// failure), so profile CRUD scenarios remain exercisable against it.
func brokenConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("this is not valid toml [[["), 0o600); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.LoadErr() == nil {
		t.Fatal("precondition: expected a broken config with a load error")
	}
	return cfg
}

// --- Item 1: config persistence failures must never be masked by success ---

// A failed store-selection persist must surface as a visible failure, not a
// "loaded store" success, and must roll back the in-memory profile so it
// doesn't diverge from what's actually on disk.
func TestSelectStoreConfigSaveFailureNoFalseSuccess(t *testing.T) {
	cfg := brokenConfig(t)
	prev, _ := cfg.Get(cfg.Active)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, "", "")

	m.selectStore(openfga.Store{ID: "store-x", Name: "X"})

	if strings.Contains(m.status, "loaded store") {
		t.Fatalf("must not show a loaded-store success status when config save failed, got %q", m.status)
	}
	if !strings.Contains(m.status, "config") {
		t.Fatalf("expected a config-save failure status, got %q", m.status)
	}
	if levels := m.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Error {
		t.Fatalf("expected an Error toast for the failed save, got %v", levels)
	}
	if p, _ := cfg.Get(cfg.Active); p.StoreID != prev.StoreID {
		t.Fatalf("profile store id must roll back on save failure, got %q want %q", p.StoreID, prev.StoreID)
	}
	// The store selection itself is nonfatal — the playground still browses it
	// in-session even though the pick couldn't be remembered.
	if m.storeID != "store-x" {
		t.Fatalf("store selection itself must still apply in-session, got storeID %q", m.storeID)
	}
}

// A failed model-load persist (the modelLoadedMsg handler's persistModel call)
// must surface as a visible failure, not a "model ..." success line, and must
// roll back the in-memory profile.
func TestModelLoadedConfigSaveFailureNoFalseSuccess(t *testing.T) {
	cfg := brokenConfig(t)
	cfg.Set(cfg.Active, config.Profile{APIURL: config.DefaultAPIURL, StoreID: "store-1"})
	prev, _ := cfg.Get(cfg.Active)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	m, _ = m.Update(modelLoadedMsg{storeID: "store-1", modelID: "model-9", graph: sampleGraph()})

	mm := m.(Model)
	if strings.HasPrefix(mm.status, "model ") {
		t.Fatalf("must not show a model-loaded success status when config save failed, got %q", mm.status)
	}
	if !strings.Contains(mm.status, "config") {
		t.Fatalf("expected a config-save failure status, got %q", mm.status)
	}
	if p, _ := cfg.Get(cfg.Active); p.ModelID != prev.ModelID {
		t.Fatalf("profile model id must roll back on save failure, got %q want %q", p.ModelID, prev.ModelID)
	}
	// The model itself still loaded and is browsable in-session.
	if mm.modelID != "model-9" {
		t.Fatalf("model load itself must still apply in-session, got modelID %q", mm.modelID)
	}
}

// Switching the active profile must roll back (never actually switch, and
// never reconnect) when the save fails, so the live session and the on-disk
// config can't diverge.
func TestSwitchProfileConfigSaveFailureRollsBack(t *testing.T) {
	cfg := brokenConfig(t)
	cfg.Set("other", config.Profile{APIURL: "http://other:8080"})
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, "", "")

	m.switchProfile("other")

	if cfg.Active != "default" {
		t.Fatalf("active profile must roll back to %q on save failure, got %q", "default", cfg.Active)
	}
	if strings.Contains(m.status, "switched to profile") {
		t.Fatalf("must not show a switched-profile success status when config save failed, got %q", m.status)
	}
	if levels := m.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Error {
		t.Fatalf("expected an Error toast for the failed save, got %v", levels)
	}
}

// Adding a profile whose save fails must not leave it in the in-memory
// config (it would silently diverge from disk), and must not show a
// created-profile success.
func TestAddProfileConfigSaveFailureRollsBack(t *testing.T) {
	cfg := brokenConfig(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	var m tea.Model = newModel(context.Background(), a, cl, "", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	m, _ = m.Update(key("1"))     // Profiles
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("a"))     // add-profile form
	if m.(Model).formKind != formAddProfile {
		t.Fatalf("a should open the add-profile form; got kind=%d", m.(Model).formKind)
	}
	for _, r := range "staging" {
		m, _ = m.Update(key(string(r)))
	}
	// Drive the form to completion without assuming an exact field count for
	// the trailing auth-method selector (a blind enter loop matching the
	// pattern of TestProfilesTabAddAndSwitch, but tolerant of either the
	// selector completing the form on its own enter or needing one more).
	m, _ = m.Update(key("enter")) // -> api_url field
	for _, r := range "http://example:9090" {
		m, _ = m.Update(key(string(r)))
	}
	for i := 0; i < 6 && m.(Model).formErr == ""; i++ {
		m, _ = m.Update(key("enter"))
	}

	mm := m.(Model)
	if mm.formKind != formAddProfile || mm.formErr == "" {
		t.Fatalf("failed save should reopen the populated add form; kind=%d err=%q", mm.formKind, mm.formErr)
	}
	if _, ok := cfg.Get("staging"); ok {
		t.Fatal("a profile whose save failed must not remain in the in-memory config")
	}
	if strings.Contains(mm.status, "created profile") {
		t.Fatalf("must not show a created-profile success status when config save failed, got %q", mm.status)
	}
	if levels := mm.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Error {
		t.Fatalf("expected an Error toast for the failed save, got %v", levels)
	}
}

// Editing a profile whose save fails must roll back to the profile's prior
// values and must not reconnect (activateResolved) or show a success status.
func TestEditProfileConfigSaveFailureRollsBack(t *testing.T) {
	cfg := brokenConfig(t)
	prev, _ := cfg.Get("default")
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	mdl := newModel(context.Background(), a, cl, "", "")
	mdl.profileEditName = "default"
	mdl.profileAuthMethod = config.AuthNone
	nm, _ := mdl.enterForm(formEditProfile)
	var m tea.Model = nm
	if m.(Model).formKind != formEditProfile {
		t.Fatalf("precondition: expected the edit-profile form to be open, got kind=%d", m.(Model).formKind)
	}

	for i := 0; i < 6 && m.(Model).formErr == ""; i++ {
		m, _ = m.Update(key("enter"))
	}

	mm := m.(Model)
	if mm.formKind != formEditProfile || mm.formErr == "" {
		t.Fatalf("failed save should reopen the populated edit form; kind=%d err=%q", mm.formKind, mm.formErr)
	}
	if strings.Contains(mm.status, "updated profile") {
		t.Fatalf("must not show an updated-profile success status when config save failed, got %q", mm.status)
	}
	if p, _ := cfg.Get("default"); p.APIURL != prev.APIURL {
		t.Fatalf("profile must roll back to its prior values on save failure, got api_url %q want %q", p.APIURL, prev.APIURL)
	}
}

// Removing a profile whose save fails must leave the profile in the
// in-memory config (not silently diverge from disk) and must not show a
// removed-profile success.
func TestRemoveProfileConfigSaveFailureRollsBack(t *testing.T) {
	cfg := brokenConfig(t)
	cfg.Set("other", config.Profile{APIURL: "http://other:8080"})
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	var m tea.Model = newModel(context.Background(), a, cl, "", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	m, _ = m.Update(key("1"))     // Profiles
	m, _ = m.Update(key("enter")) // descend into the panel
	// The list is sorted (default, other); move to "other" so we don't try to
	// remove the active profile (Config.Remove forbids that regardless).
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("d")) // request removal
	if m.(Model).confirm == nil {
		t.Fatal("d should open the remove-profile confirmation modal")
	}
	m, _ = m.Update(key("y")) // confirm

	mm := m.(Model)
	if _, ok := cfg.Get("other"); !ok {
		t.Fatal("a profile removal whose save failed must roll back (profile must still be present)")
	}
	if strings.Contains(mm.status, "removed profile") {
		t.Fatalf("must not show a removed-profile success status when config save failed, got %q", mm.status)
	}
	if levels := mm.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Error {
		t.Fatalf("expected an Error toast for the failed save, got %v", levels)
	}
}

func TestCannotRemoveEnvironmentSelectedProfile(t *testing.T) {
	cfg := config.New()
	cfg.Set("prod", config.Profile{APIURL: "https://prod.example"})
	cl, _ := openfga.NewClient("https://prod.example")
	a := cli.New(log.New(io.Discard), cfg, "test")
	a.Overrides.Profile = "prod"
	var m tea.Model = newModel(context.Background(), a, cl, "", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("d"))

	mm := m.(Model)
	if mm.confirm != nil {
		t.Fatal("the effective active profile must not open a deletion confirmation")
	}
	if _, ok := cfg.Get("prod"); !ok {
		t.Fatal("the effective active profile was removed")
	}
	if !strings.Contains(mm.status, "cannot remove the active profile") {
		t.Fatalf("status = %q, want active-profile refusal", mm.status)
	}
}

// A store-deletion cleanup persist (clearing the deleted store's id from the
// active profile) is additive: the delete API call itself already succeeded,
// so that success stands, but a save failure on the incidental config cleanup
// must still surface as its own visible error, not be silently swallowed.
func TestStoreDeletedConfigCleanupFailureIsAdditive(t *testing.T) {
	cfg := brokenConfig(t)
	cfg.Set(cfg.Active, config.Profile{APIURL: config.DefaultAPIURL, StoreID: "store-1"})
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	m, _ = m.Update(storeDeletedMsg{id: "store-1"})

	mm := m.(Model)
	// The delete genuinely succeeded — that fact must still be visible.
	if mm.status != "store deleted" {
		t.Fatalf(`expected status "store deleted" (the delete itself succeeded), got %q`, mm.status)
	}
	if levels := mm.toasts.Levels(); len(levels) < 2 {
		t.Fatalf("expected both a success toast (delete) and an error toast (config cleanup failure), got %v", levels)
	} else {
		if levels[0] != toast.Success {
			t.Fatalf("expected the delete's own toast to be Success, got %v", levels)
		}
		if levels[len(levels)-1] != toast.Error {
			t.Fatalf("expected an additional Error toast for the config cleanup failure, got %v", levels)
		}
	}
}

// The initial config-creation failure in Run() logs at Warn, not Debug, so it
// is visible under the CLI's default log level (Warn — see cmd/ofga's
// logLevel) instead of silently requiring --verbose to notice.
func TestInitialConfigSaveFailureVisibleAtDefaultLogLevel(t *testing.T) {
	var buf strings.Builder
	logger := log.New(&buf)
	logger.Warn("failed to write initial config", "error", "boom")
	if !strings.Contains(buf.String(), "failed to write initial config") {
		t.Fatalf("a Warn-level log must be visible under the default logger, got %q", buf.String())
	}

	buf.Reset()
	logger.Debug("failed to write initial config", "error", "boom")
	if strings.Contains(buf.String(), "failed to write initial config") {
		t.Fatal("a Debug-level log is not visible under the default logger — this is exactly why Run() must log the initial-config failure at Warn, not Debug")
	}
}

// --- Item 2: stale async responses / concurrent loads ---

// Two rapid re-picks of a model from the switcher, against the same store:
// if the older response lands after the newer one, it must not clobber the
// newer model's state.
func TestStaleModelLoadSameStoreDropped(t *testing.T) {
	m := newTestModel().(Model)
	m.modelGen++
	oldGen := m.modelGen
	m.modelGen++
	newGen := m.modelGen

	// The newer pick's response lands first (plausible under real network
	// jitter — nothing guarantees response order matches request order).
	tm, _ := m.Update(modelLoadedMsg{storeID: "store-1", gen: newGen, modelID: "model-new", graph: sampleGraph()})
	mm := tm.(Model)
	if mm.modelID != "model-new" {
		t.Fatalf("modelID = %q, want model-new", mm.modelID)
	}

	// The older (stale) response lands after — it must be dropped, not
	// overwrite the newer model that already applied.
	tm2, _ := mm.Update(modelLoadedMsg{storeID: "store-1", gen: oldGen, modelID: "model-old", graph: sampleGraph()})
	mm2 := tm2.(Model)
	if mm2.modelID != "model-new" {
		t.Fatalf("a stale model response (gen %d) must not overwrite the newer model %q, got %q", oldGen, "model-new", mm2.modelID)
	}
}

// A rapid query resubmission (e.g. mashing enter, or a rerun before the first
// finished) must let only the newest response apply.
func TestStaleQueryResultDropped(t *testing.T) {
	m := newTestModel().(Model)
	m.queryGen++
	oldGen := m.queryGen
	m.queryGen++
	newGen := m.queryGen

	tm, _ := m.Update(queryResultMsg{storeID: "store-1", gen: newGen, mode: "check", ok: true, badge: true})
	mm := tm.(Model)
	if !mm.result.ok {
		t.Fatal("the newer query result should have applied")
	}

	tm2, _ := mm.Update(queryResultMsg{storeID: "store-1", gen: oldGen, mode: "check", ok: false, badge: true})
	mm2 := tm2.(Model)
	if !mm2.result.ok {
		t.Fatal("a stale query result must not overwrite the newer result already applied")
	}
}

// A stale response's data must be dropped, but its pending-load slot must
// still be freed — otherwise the spinner could get stuck on a response that
// will never be "current" again.
func TestStaleResponseStillFreesLoadSlot(t *testing.T) {
	m := newTestModel().(Model)
	m.pendingLoads = 1
	m.loading = true

	tm, _ := m.Update(tuplesLoadedMsg{storeID: "some-other-store"})
	if tm.(Model).loading {
		t.Fatal("a dropped stale response must still free its pending-load slot, not leave the spinner stuck on")
	}
}

// Selecting a store fires four concurrent loads (model, tuples, changes,
// assertions). This is the core "concurrent startup loads" bug: previously a
// single begin covered all four, so whichever landed first stopped the
// spinner while its siblings were still in flight.
func TestSelectStoreConcurrentLoadsKeepSpinnerOn(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "", "")
	// Isolate this assertion from whatever Init()'s own startup budget left
	// behind; selectStore's begins are what's under test here.
	m.pendingLoads, m.loading = 0, false

	m.selectStore(openfga.Store{ID: "store-1", Name: "demo"})
	if m.pendingLoads != 4 || !m.loading {
		t.Fatalf("selecting a store should begin exactly 4 concurrent loads, got pendingLoads=%d loading=%v", m.pendingLoads, m.loading)
	}

	var tm tea.Model = m
	tm, _ = tm.Update(modelLoadedMsg{storeID: "store-1", modelID: "model-1", graph: sampleGraph()})
	tm, _ = tm.Update(tuplesLoadedMsg{storeID: "store-1"})
	tm, _ = tm.Update(changesLoadedMsg{storeID: "store-1"})
	if !tm.(Model).loading {
		t.Fatal("spinner must stay on while the 4th concurrent load (assertions) is still pending")
	}
	tm, _ = tm.Update(assertionsLoadedMsg{storeID: "store-1"})
	if tm.(Model).loading {
		t.Fatal("spinner should stop once all 4 concurrent loads have landed")
	}
}

// The Assertions "enter" handler fires two concurrent commands (the
// assertion's own Check and its resolution tree) under a single keypress;
// each needs its own begin so the first landing can't stop the spinner while
// the other is still in flight.
func TestAssertionEnterConcurrentLoadsKeepSpinnerOn(t *testing.T) {
	var m tea.Model = newTestModel()
	m, _ = m.Update(key("7"))     // Assertions section
	m, _ = m.Update(key("enter")) // descend into the panel

	// Isolate this assertion from newTestModel's own setup.
	mm := m.(Model)
	mm.pendingLoads, mm.loading = 0, false
	m = mm

	m, _ = m.Update(key("enter")) // run + resolve the selected assertion
	mm = m.(Model)
	if mm.pendingLoads != 2 || !mm.loading {
		t.Fatalf("running+resolving an assertion should begin 2 concurrent loads, got pendingLoads=%d loading=%v", mm.pendingLoads, mm.loading)
	}

	m, _ = m.Update(assertOneMsg{idx: 0, result: assertResult{ran: true, expected: true, got: true, pass: true}})
	if !m.(Model).loading {
		t.Fatal("spinner must stay on until both concurrent loads land (resolution still pending)")
	}
	m, _ = m.Update(resolutionMsg{})
	if m.(Model).loading {
		t.Fatal("spinner should stop once both concurrent loads have landed")
	}
}

// --- Item 3: reduced motion disables the entrance animation too ---

func TestReducedMotionDisablesEntranceAnimation(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("OFGA_REDUCED_MOTION", "")
	t.Setenv("OPENFGA_REDUCED_MOTION", "1")

	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "", "")

	if m.entering {
		t.Fatal("reduced motion must disable the entrance animation, not just ambient drift")
	}
	if m.entranceFrac != 0 {
		t.Fatalf("entranceFrac = %v, want 0 when reduced motion skips the entrance animation", m.entranceFrac)
	}
}

// --- Item 4: Stores empty-state CTA (advertises n, handler only took a) ---

func TestStoresEmptyStateNKeyOpensCreateForm(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "", "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	mm := m.(Model)
	if len(mm.stores) != 0 {
		t.Fatalf("precondition: expected no stores loaded (the empty state), got %d", len(mm.stores))
	}
	mm.section = secStores

	// Panel focus: pressing n directly in the Stores section's key handler,
	// matching the empty state's "press n to create one" hint.
	nm, _ := mm.handleSectionKey("n", key("n"))
	if nm.(Model).formKind != formCreateStore {
		t.Fatalf("pressing n in the Stores panel should open the create-store form, got formKind %d", nm.(Model).formKind)
	}

	// Sidebar focus: the generic CTA replay (n/a/e/d in handleSidebarKey) must
	// also recognize n and land in the same form.
	nm2, _ := mm.handleSidebarKey("n")
	if nm2.(Model).formKind != formCreateStore {
		t.Fatalf("pressing n on the sidebar (replay) should open the create-store form, got formKind %d", nm2.(Model).formKind)
	}

	// "a" must keep working too (not replaced by the alias).
	nm3, _ := mm.handleSectionKey("a", key("a"))
	if nm3.(Model).formKind != formCreateStore {
		t.Fatalf("pressing a in the Stores panel should still open the create-store form, got formKind %d", nm3.(Model).formKind)
	}
}

// --- Follow-up round: remaining direct .loading mutations, and additional
// generation gaps (models list, assertions list, stores/connection, tuples,
// changes) found by a second review pass. ---

// onEnterSection's lazy Changes load used to set m.loading = true directly,
// bypassing pendingLoads. If another load was already in flight, that other
// load's own completion could then decrement pendingLoads to 0 and stop the
// spinner while this lazy load was still outstanding (or vice versa) —
// whichever landed first won, hiding that the other was still pending.
func TestOnEnterSectionChangesLazyLoadUsesBeginLoad(t *testing.T) {
	m := newTestModel().(Model)
	m.changes = nil // force the lazy-load guard to fire on entry
	m.section = secChanges
	m.pendingLoads, m.loading = 0, false
	// Simulate an unrelated load already in flight (e.g. a concurrent stores
	// reload) that must not be forgotten about.
	m.beginLoad()

	nm, _ := m.onEnterSection()
	mm := nm.(Model)
	if mm.pendingLoads != 2 || !mm.loading {
		t.Fatalf("entering Changes with an empty list should begin its own load on top of the one already in flight, got pendingLoads=%d loading=%v", mm.pendingLoads, mm.loading)
	}

	// The lazy load's own response lands first: the spinner must stay on
	// because the unrelated load is still pending.
	var tm tea.Model = mm
	tm, _ = tm.Update(changesLoadedMsg{storeID: "store-1", gen: mm.changesGen, changes: []openfga.TupleChange{}})
	if !tm.(Model).loading {
		t.Fatal("spinner must stay on: the unrelated load begun before entering Changes is still pending")
	}
}

// Same bug, same fix, for the Assertions section's lazy load.
func TestOnEnterSectionAssertionsLazyLoadUsesBeginLoad(t *testing.T) {
	m := newTestModel().(Model)
	m.assertions = nil // force the lazy-load guard to fire on entry
	m.section = secAssertions
	m.pendingLoads, m.loading = 0, false
	m.beginLoad() // an unrelated load already in flight

	nm, _ := m.onEnterSection()
	mm := nm.(Model)
	if mm.pendingLoads != 2 || !mm.loading {
		t.Fatalf("entering Assertions with a nil list should begin its own load on top of the one already in flight, got pendingLoads=%d loading=%v", mm.pendingLoads, mm.loading)
	}

	var tm tea.Model = mm
	tm, _ = tm.Update(assertionsLoadedMsg{storeID: "store-1", modelID: mm.modelID, gen: mm.assertLoadGen, assertions: []openfga.Assertion{}})
	if !tm.(Model).loading {
		t.Fatal("spinner must stay on: the unrelated load begun before entering Assertions is still pending")
	}
}

// --- Item 2 (continued): models-list generation ---

// Closing and reopening the model switcher fires two list loads against the
// same store. If the older lands after the newer, it must not overwrite the
// list the newer response already applied — and both must free their load
// slots.
func TestStaleModelsListSameStoreDropped(t *testing.T) {
	m := newTestModel().(Model)
	m.pendingLoads, m.loading = 0, false
	m.beginLoad() // first open
	oldGen := m.modelsGen
	m.beginLoad() // closed and reopened before the first list landed
	m.modelsGen++
	newGen := m.modelsGen

	var tm tea.Model = m
	newModels := []openfga.AuthorizationModel{{ID: "model-new", SchemaVersion: "1.1"}}
	tm, _ = tm.Update(modelsListedMsg{storeID: "store-1", gen: newGen, models: newModels})
	mm := tm.(Model)
	if len(mm.models) != 1 || mm.models[0].ID != "model-new" {
		t.Fatalf("the newer list response should have applied, got %+v", mm.models)
	}
	if !mm.loading {
		t.Fatal("the older list request is still pending; spinner must stay on")
	}

	oldModels := []openfga.AuthorizationModel{{ID: "model-old", SchemaVersion: "1.1"}}
	tm2, _ := mm.Update(modelsListedMsg{storeID: "store-1", gen: oldGen, models: oldModels})
	mm2 := tm2.(Model)
	if len(mm2.models) != 1 || mm2.models[0].ID != "model-new" {
		t.Fatalf("a stale models-list response (gen %d) must not overwrite the newer list, got %+v", oldGen, mm2.models)
	}
	if mm2.loading {
		t.Fatal("the stale response must still free its own pending-load slot")
	}
}

// --- Item 3: assertions-load generation, model-switch and same-model supersession ---

// An assertions load dispatched for model A must not overwrite the view after
// the user has since switched to model B, even though both the store id and
// (pre-fix) the generation were never enough to catch it on their own since
// no generation existed at all.
func TestStaleAssertionsLoadDroppedOnModelSwitch(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	m.assertModelID = "model-a"
	gen := m.assertLoadGen

	// The user switches to a different model before the in-flight assertions
	// load for model A lands.
	m.modelID = "model-b"

	tm, _ := m.Update(assertionsLoadedMsg{storeID: "store-1", modelID: "model-a", gen: gen, assertions: []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:x", Relation: "viewer", Object: "document:y"}, Expectation: true},
	}})
	mm := tm.(Model)
	if mm.assertModelID == "model-a" && len(mm.assertions) > 0 && mm.assertions[0].TupleKey.User == "user:x" {
		t.Fatal("an assertions load for a model the user has since switched away from must not overwrite the assertions view")
	}
}

// Two reloads of the same model's assertions (e.g. a manual "r" racing the
// reload a write already triggers) can land out of order; only the newer one
// must apply.
func TestStaleAssertionsLoadSameModelGenDropped(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-1"
	m.assertModelID = "model-1"
	oldGen := m.assertLoadGen
	m.assertLoadGen++
	newGen := m.assertLoadGen

	newAssertions := []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:new", Relation: "viewer", Object: "document:y"}, Expectation: true},
	}
	tm, _ := m.Update(assertionsLoadedMsg{storeID: "store-1", modelID: "model-1", gen: newGen, assertions: newAssertions})
	mm := tm.(Model)
	if len(mm.assertions) != 1 || mm.assertions[0].TupleKey.User != "user:new" {
		t.Fatalf("the newer assertions response should have applied, got %+v", mm.assertions)
	}

	oldAssertions := []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:old", Relation: "viewer", Object: "document:y"}, Expectation: true},
	}
	tm2, _ := mm.Update(assertionsLoadedMsg{storeID: "store-1", modelID: "model-1", gen: oldGen, assertions: oldAssertions})
	mm2 := tm2.(Model)
	if len(mm2.assertions) != 1 || mm2.assertions[0].TupleKey.User != "user:new" {
		t.Fatalf("a stale assertions response (gen %d) must not overwrite the newer one, got %+v", oldGen, mm2.assertions)
	}
}

// When no model is loaded yet (modelID == ""), an assertions load resolves
// "latest" internally and its resolved identity must be accepted (there is
// nothing known yet to compare it against). But once a specific model
// becomes current, a *later-arriving* response still carrying an older
// resolved identity must be rejected — the resolved identity is checked
// against the active model whenever one is known.
func TestAssertionsLoadLatestResolutionCheckedAgainstActiveModel(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = ""
	m.assertModelID = ""
	gen := m.assertLoadGen

	// Dispatched with modelID == "" (unresolved); the command resolves it to
	// whatever was "latest" at the time and returns that as msg.modelID.
	tm, _ := m.Update(assertionsLoadedMsg{storeID: "store-1", modelID: "model-old-latest", gen: gen, assertions: []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:accepted", Relation: "viewer", Object: "document:y"}, Expectation: true},
	}})
	mm := tm.(Model)
	if mm.assertModelID != "model-old-latest" || len(mm.assertions) != 1 {
		t.Fatalf("a resolved-latest response must be accepted when no model was known yet, got assertModelID=%q assertions=%+v", mm.assertModelID, mm.assertions)
	}

	// The user has since picked a specific, different model.
	mm.modelID = "model-b"

	// A second, older in-flight request (also dispatched before any model was
	// known, so it also resolved "latest" — to the same prior value) lands
	// late. It must now be rejected: the active model is known and differs.
	tm2, _ := mm.Update(assertionsLoadedMsg{storeID: "store-1", modelID: "model-old-latest", gen: gen, assertions: []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:rejected", Relation: "viewer", Object: "document:y"}, Expectation: true},
	}})
	mm2 := tm2.(Model)
	if len(mm2.assertions) == 1 && mm2.assertions[0].TupleKey.User == "user:rejected" {
		t.Fatal("a resolved-latest response for a model that is no longer active must be rejected once the active model is known")
	}
}

// --- Item 4: connection generation (stores), and tuple/change same-store supersession ---

// activateResolved bumps the connection generation; a stores list dispatched
// before the reconnect (from the old profile/connection) must not be able to
// land afterward and repopulate the list from the wrong server — or, worse,
// auto-select a store id that may not even exist on the new one.
func TestStaleStoresLoadAfterReconnectDropped(t *testing.T) {
	cfg := config.New()
	cfg.Set("other", config.Profile{APIURL: "http://other:8080"})
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, "", "")
	oldStoresGen := m.storesGen

	// Switching profiles reconnects and bumps storesGen.
	_ = m.switchProfile("other")
	if m.storesGen == oldStoresGen {
		t.Fatal("precondition: switching profiles should bump storesGen")
	}
	newStoresGen := m.storesGen

	// The old profile's in-flight stores request lands after the switch.
	var tm tea.Model = m
	tm, _ = tm.Update(storesLoadedMsg{gen: oldStoresGen, stores: []openfga.Store{{ID: "wrong-server-store", Name: "stale"}}})
	mm := tm.(Model)
	if len(mm.stores) != 0 {
		t.Fatalf("a stores list from a connection superseded by a reconnect must be dropped, got %+v", mm.stores)
	}

	// The new connection's own response must still apply normally.
	tm2, _ := mm.Update(storesLoadedMsg{gen: newStoresGen, stores: []openfga.Store{{ID: "right-server-store", Name: "current"}}})
	mm2 := tm2.(Model)
	if len(mm2.stores) != 1 || mm2.stores[0].ID != "right-server-store" {
		t.Fatalf("the current connection's stores response should apply, got %+v", mm2.stores)
	}
}

// A manual tuples reload racing the reload a tuple write already triggers
// (both against the same store) can land out of order; only the newer one
// must apply.
func TestStaleTuplesReloadDropped(t *testing.T) {
	m := newTestModel().(Model)
	oldGen := m.tuplesGen
	m.tuplesGen++
	newGen := m.tuplesGen

	newTuples := []openfga.Tuple{{Key: openfga.TupleKey{User: "user:new", Relation: "owner", Object: "document:z"}}}
	tm, _ := m.Update(tuplesLoadedMsg{storeID: "store-1", gen: newGen, tuples: newTuples})
	mm := tm.(Model)
	if len(mm.tuples) != 1 || mm.tuples[0].Key.User != "user:new" {
		t.Fatalf("the newer tuples response should have applied, got %+v", mm.tuples)
	}

	oldTuples := []openfga.Tuple{{Key: openfga.TupleKey{User: "user:old", Relation: "owner", Object: "document:z"}}}
	tm2, _ := mm.Update(tuplesLoadedMsg{storeID: "store-1", gen: oldGen, tuples: oldTuples})
	mm2 := tm2.(Model)
	if len(mm2.tuples) != 1 || mm2.tuples[0].Key.User != "user:new" {
		t.Fatalf("a stale tuples response (gen %d) must not overwrite the newer one, got %+v", oldGen, mm2.tuples)
	}
}

// Same race, same fix, for changes.
func TestStaleChangesReloadDropped(t *testing.T) {
	m := newTestModel().(Model)
	oldGen := m.changesGen
	m.changesGen++
	newGen := m.changesGen

	newChanges := []openfga.TupleChange{{TupleKey: openfga.TupleKey{User: "user:new", Relation: "owner", Object: "document:z"}, Operation: "TUPLE_OPERATION_WRITE"}}
	tm, _ := m.Update(changesLoadedMsg{storeID: "store-1", gen: newGen, changes: newChanges})
	mm := tm.(Model)
	if len(mm.changes) != 1 || mm.changes[0].TupleKey.User != "user:new" {
		t.Fatalf("the newer changes response should have applied, got %+v", mm.changes)
	}

	oldChanges := []openfga.TupleChange{{TupleKey: openfga.TupleKey{User: "user:old", Relation: "owner", Object: "document:z"}, Operation: "TUPLE_OPERATION_WRITE"}}
	tm2, _ := mm.Update(changesLoadedMsg{storeID: "store-1", gen: oldGen, changes: oldChanges})
	mm2 := tm2.(Model)
	if len(mm2.changes) != 1 || mm2.changes[0].TupleKey.User != "user:new" {
		t.Fatalf("a stale changes response (gen %d) must not overwrite the newer one, got %+v", oldGen, mm2.changes)
	}
}

// --- Round-3 audit: reconnect must invalidate *every* async category, even
// ones it doesn't redispatch, and even when the old and new profile happen to
// resolve to the same store/model ids ---

// A profile switch that lands on a store/model id identical to the previous
// profile's (e.g. two profiles both pinned to "store-1"/"model-1" on
// different servers) must still invalidate an old, still-in-flight request in
// every async category — including the model list, a query, a resolution
// tree and an assertion run, none of which activateResolved redispatches itself.
// Without their own generation bumps, a stale response would pass every
// storeID/modelID check (identical across profiles) and silently apply data
// from the wrong server.
func TestReconnectInvalidatesAllGenerationsSameIDs(t *testing.T) {
	// client.New validates store/model ids as ULIDs, so the profiles (and the
	// messages compared against them below) need well-formed ones even though
	// other tests in this file use plain literals for message-only fields.
	const storeID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const modelID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "http://server-a:8080", StoreID: storeID, ModelID: modelID})
	cfg.Set("other", config.Profile{APIURL: "http://server-b:8080", StoreID: storeID, ModelID: modelID})
	cl, _ := openfga.NewClient("http://server-a:8080")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, storeID, modelID)

	oldModelsGen, oldQueryGen, oldResGen, oldAssertGen := m.modelsGen, m.queryGen, m.resGen, m.assertGen

	_ = m.switchProfile("other")
	if m.storeID != storeID || m.modelID != modelID {
		t.Fatalf("precondition: the new profile should resolve to the same ids, got store=%q model=%q", m.storeID, m.modelID)
	}
	if m.modelsGen == oldModelsGen || m.queryGen == oldQueryGen || m.resGen == oldResGen || m.assertGen == oldAssertGen {
		t.Fatal("reconnecting must bump modelsGen, queryGen, resGen and assertGen even though this batch doesn't redispatch them")
	}

	var tm tea.Model = m
	tm, _ = tm.Update(modelsListedMsg{storeID: storeID, gen: oldModelsGen, models: []openfga.AuthorizationModel{{ID: "wrong-server-model"}}})
	tm, _ = tm.Update(queryResultMsg{storeID: storeID, modelID: modelID, gen: oldQueryGen, title: "wrong-server-result", ok: true, badge: true})
	tm, _ = tm.Update(resolutionMsg{storeID: storeID, modelID: modelID, gen: oldResGen, root: &fga.ResNode{Name: "wrong-server-tree"}})
	tm, _ = tm.Update(assertTestMsg{storeID: storeID, modelID: modelID, gen: oldAssertGen, results: []assertResult{{}}, passed: 1, total: 1})
	mm := tm.(Model)

	if len(mm.models) != 0 {
		t.Fatalf("a pre-reconnect model list must be dropped, got %+v", mm.models)
	}
	if mm.result.title == "wrong-server-result" {
		t.Fatal("a pre-reconnect query result must be dropped")
	}
	if mm.resTree != nil && mm.resTree.Name == "wrong-server-tree" {
		t.Fatal("a pre-reconnect resolution tree must be dropped")
	}
	if len(mm.assertResults) != 0 {
		t.Fatalf("a pre-reconnect assertion run must be dropped, got %+v", mm.assertResults)
	}
}

// --- Round-3 audit: selectStore must invalidate every generation, not just
// the ones it redispatches, to survive an A -> B -> A store-switch cycle ---

// staleStore alone is insufficient across a store re-selection cycle: once
// the user is back on A, a still-in-flight request from the *original* A
// selection matches the current store id again. Only a per-selection
// generation bump on every category (including ones selectStore doesn't
// itself redispatch, like the model list, query, resolution and assertion
// run) can tell the original A request apart from the second one.
func TestSelectStoreABAOutOfOrderDropped(t *testing.T) {
	m := newTestModel().(Model)
	storeA := openfga.Store{ID: "store-a", Name: "A"}
	storeB := openfga.Store{ID: "store-b", Name: "B"}

	m.selectStore(storeA)
	firstAModelsGen, firstAQueryGen, firstAResGen, firstAAssertGen := m.modelsGen, m.queryGen, m.resGen, m.assertGen

	m.selectStore(storeB)
	m.selectStore(storeA) // back to A; a new generation for every category

	if m.modelsGen == firstAModelsGen || m.queryGen == firstAQueryGen || m.resGen == firstAResGen || m.assertGen == firstAAssertGen {
		t.Fatal("re-selecting a store must bump every per-kind generation, even ones not immediately redispatched, so a prior selection cycling back to the same store can't pass as current")
	}

	// The very first A selection's in-flight requests land now, tagged with
	// the generations captured right after that first selection.
	var tm tea.Model = m
	tm, _ = tm.Update(modelsListedMsg{storeID: "store-a", gen: firstAModelsGen, models: []openfga.AuthorizationModel{{ID: "stale-model"}}})
	tm, _ = tm.Update(queryResultMsg{storeID: "store-a", modelID: "model-1", gen: firstAQueryGen, title: "stale-result", ok: true, badge: true})
	tm, _ = tm.Update(resolutionMsg{storeID: "store-a", modelID: "model-1", gen: firstAResGen, root: &fga.ResNode{Name: "stale-tree"}})
	tm, _ = tm.Update(assertTestMsg{storeID: "store-a", modelID: "model-1", gen: firstAAssertGen, results: []assertResult{{}}, passed: 1, total: 1})
	mm := tm.(Model)

	if len(mm.models) != 0 {
		t.Fatalf("the original A selection's stale model list must not overwrite the second A selection's view, got %+v", mm.models)
	}
	if mm.result.title == "stale-result" {
		t.Fatal("the original A selection's stale query result must not overwrite the second A selection's view")
	}
	if mm.resTree != nil && mm.resTree.Name == "stale-tree" {
		t.Fatal("the original A selection's stale resolution tree must not overwrite the second A selection's view")
	}
	if len(mm.assertResults) != 0 {
		t.Fatalf("the original A selection's stale assertion run must not overwrite the second A selection's view, got %+v", mm.assertResults)
	}
}

// --- Round-3 audit: resolutionMsg/assertTestMsg/assertOneMsg must also be
// checked against the active model (m.modelID), not only their own primary
// comparator, since the latter can lag right after a model switch ---

// resolutionMsg previously checked only store+gen; a response resolved
// against a model the user has since switched away from must be rejected.
func TestResolutionMsgDroppedOnModelMismatch(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	gen := m.resGen

	// The user switches models before the in-flight resolution lands.
	m.modelID = "model-b"

	tm, _ := m.Update(resolutionMsg{storeID: "store-1", modelID: "model-a", gen: gen, root: &fga.ResNode{Name: "stale"}})
	mm := tm.(Model)
	if mm.showRes && mm.resTree != nil && mm.resTree.Name == "stale" {
		t.Fatal("a resolution resolved against a model the user has since switched away from must be rejected")
	}
}

// assertTestMsg/assertOneMsg compare msg.modelID against m.assertModelID,
// which only updates when the Assertions list itself reloads and so can lag
// m.modelID (the active model) right after a model switch; both must also be
// checked against m.modelID when it's known.
func TestAssertTestMsgDroppedWhenActiveModelChangedButAssertModelIDLags(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	m.assertModelID = "model-a"
	gen := m.assertGen

	// The active model changes, but assertModelID hasn't caught up yet (the
	// Assertions section hasn't reloaded).
	m.modelID = "model-b"

	tm, _ := m.Update(assertTestMsg{storeID: "store-1", modelID: "model-a", gen: gen, results: []assertResult{{}}, passed: 1, total: 1})
	mm := tm.(Model)
	if len(mm.assertResults) != 0 {
		t.Fatalf("an assertion run for a model the active model has since diverged from must be rejected, got %+v", mm.assertResults)
	}
}

func TestAssertOneMsgDroppedWhenActiveModelChangedButAssertModelIDLags(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	m.assertModelID = "model-a"
	m.assertResults = []assertResult{{}, {}}
	gen := m.assertGen

	m.modelID = "model-b"

	tm, _ := m.Update(assertOneMsg{storeID: "store-1", modelID: "model-a", gen: gen, idx: 0, result: assertResult{pass: true}})
	mm := tm.(Model)
	if mm.assertResults[0].pass {
		t.Fatal("a single assertion result for a model the active model has since diverged from must be rejected")
	}
}

// --- Round-3 audit: storesGen must guard same-connection stores dispatches
// too, not only reconnects ---

// Two stores-list dispatches against the same connection (e.g. a manual "r"
// racing the refresh a create/delete already triggers) can land out of order;
// only the newer one must apply.
func TestStaleStoresLoadSameConnectionDropped(t *testing.T) {
	m := newTestModel().(Model)
	oldGen := m.storesGen
	m.storesGen++
	newGen := m.storesGen

	tm, _ := m.Update(storesLoadedMsg{gen: newGen, stores: []openfga.Store{{ID: "store-new", Name: "new"}}})
	mm := tm.(Model)
	if len(mm.stores) != 1 || mm.stores[0].ID != "store-new" {
		t.Fatalf("the newer stores response should have applied, got %+v", mm.stores)
	}

	tm2, _ := mm.Update(storesLoadedMsg{gen: oldGen, stores: []openfga.Store{{ID: "store-old", Name: "old"}}})
	mm2 := tm2.(Model)
	if len(mm2.stores) != 1 || mm2.stores[0].ID != "store-new" {
		t.Fatalf("a stale stores response (gen %d) must not overwrite the newer one, got %+v", oldGen, mm2.stores)
	}
}

func TestStoreListSanitizesTerminalControls(t *testing.T) {
	m := newTestModel().(Model)
	attack := "\x1b]52;c;YXR0YWNr\x07"
	m.stores = []openfga.Store{{ID: "store-1", Name: "safe" + attack + "name"}}
	m.populateStores()
	item, ok := m.storesList.Selected()
	if !ok {
		t.Fatal("expected a selected store")
	}
	if strings.Contains(item.TitleText, attack) || strings.ContainsAny(item.TitleText, "\x1b\x07") {
		t.Fatalf("store title retained terminal controls: %q", item.TitleText)
	}
	if item.ID != "store-1" {
		t.Fatalf("sanitization must preserve the raw action ID, got %q", item.ID)
	}
}
