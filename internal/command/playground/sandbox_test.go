package playground

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

const sandboxEndpoint = "http://127.0.0.1:12399"

func sandboxWS(names ...string) *modeltest.Workspace {
	ws := &modeltest.Workspace{}
	for range names {
		ws.TestFiles = append(ws.TestFiles, &modeltest.TestFile{})
	}
	return ws
}

func newTestSandbox(t *testing.T) (Model, *cli.CLI, map[string]*modeltest.Workspace) {
	t.Helper()
	configtest.Isolate(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	a := cli.New(log.New(io.Discard), cfg, "test")
	cl, _ := openfga.NewClient(sandboxEndpoint)
	wss := map[string]*modeltest.Workspace{"github": sandboxWS("a", "b"), "gdrive": sandboxWS("c")}
	m := newSandboxModel(context.Background(), a, cl, SandboxOptions{Endpoint: sandboxEndpoint, Workspaces: wss})
	return m, a, wss
}

func TestSandboxModel_ProfileAndSection(t *testing.T) {
	m, a, _ := newTestSandbox(t)
	if m.profile != "playground" {
		t.Fatalf("profile = %q, want playground", m.profile)
	}
	if m.section != secStores {
		t.Fatalf("section = %v, want secStores", m.section)
	}
	if m.storeID != "" {
		t.Fatalf("storeID = %q, want none preselected", m.storeID)
	}
	if _, ok := a.Config.Get("playground"); ok {
		t.Fatal("playground profile must not be in cli.Config")
	}
	if !strings.Contains(m.bootNotice, "playground at "+sandboxEndpoint) || strings.Contains(m.bootNotice, "seeded") {
		t.Fatalf("bootNotice = %q", m.bootNotice)
	}
}

func TestSandboxModel_StoreSelectionSwapsWorkspace(t *testing.T) {
	m, _, wss := newTestSandbox(t)
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	if m.wb.workspace != wss["github"] || len(m.wb.files) != 2 {
		t.Fatalf("github workspace not applied: %v files=%d", m.wb.workspace, len(m.wb.files))
	}
	m.wb.results = []modeltest.TestResult{{Name: "x"}}
	m.wb.treeSel = 1
	m.selectStore(openfga.Store{ID: "s2", Name: "unknown"})
	if m.wb.workspace != nil || len(m.wb.files) != 0 || len(m.wb.results) != 0 || m.wb.treeSel != 0 {
		t.Fatalf("workbench not cleared: ws=%v files=%d results=%d sel=%d", m.wb.workspace, len(m.wb.files), len(m.wb.results), m.wb.treeSel)
	}
	if m.ephemeralStoreID != "s2" {
		t.Fatalf("ephemeralStoreID = %q, want s2", m.ephemeralStoreID)
	}
	// First-store adoption goes through selectStore too.
	m2, _, wss2 := newTestSandbox(t)
	nm, _ := m2.Update(storesLoadedMsg{gen: m2.storesGen, stores: []openfga.Store{{ID: "g", Name: "gdrive"}}})
	if got := nm.(Model); got.storeID != "g" || got.wb.workspace != wss2["gdrive"] {
		t.Fatalf("adoption: storeID=%q ws=%v", got.storeID, got.wb.workspace)
	}
}

func TestSeededModel_KeepsWorkspaceOnStoreSelect(t *testing.T) {
	configtest.Isolate(t)
	cfg, _ := config.Load()
	a := cli.New(log.New(io.Discard), cfg, "test")
	cl, _ := openfga.NewClient(sandboxEndpoint)
	ws := sandboxWS("a")
	m := newSeededModel(context.Background(), a, SeedOptions{Client: cl, StoreID: "x", Endpoint: sandboxEndpoint, Workspace: ws})
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	if m.wb.workspace != ws || len(m.wb.files) != 1 {
		t.Fatal("seeded session must keep its single workspace")
	}
	if m.profile != ephemeralProfileName {
		t.Fatalf("profile = %q", m.profile)
	}
}

func TestSandboxModel_ProfileSwitchConfig(t *testing.T) {
	m, a, wss := newTestSandbox(t)
	a.Config.Set("real", config.Profile{APIURL: "http://127.0.0.1:1"})
	if err := a.Config.Save(); err != nil {
		t.Fatal(err)
	}
	// Adoption on the playground must not persist.
	m.selectStore(openfga.Store{ID: "sbx-store", Name: "github"})

	m.switchProfile("real")
	if a.Config.Active != "real" {
		t.Fatalf("active = %q, want real", a.Config.Active)
	}
	on, _ := config.Load()
	if on.Active != "real" {
		t.Fatalf("saved active = %q, want real", on.Active)
	}
	// Leaving the playground clears the workbench with no store pick.
	if m.wb.workspace != nil || len(m.wb.files) != 0 {
		t.Fatal("real profile must not keep sample workspace")
	}

	m.switchProfile("playground")
	if m.profile != "playground" || m.storeID != "sbx-store" {
		t.Fatalf("profile=%q store=%q", m.profile, m.storeID)
	}
	nm, _ := m.Update(storesLoadedMsg{gen: m.storesGen, stores: []openfga.Store{{ID: "sbx-store", Name: "github"}}})
	m = nm.(Model)
	if m.wb.workspace != wss["github"] {
		t.Fatal("workspace not re-applied on playground")
	}
	// A later stores reload (refresh, store created) keeps the run's results.
	m.wb.results = []modeltest.TestResult{{}}
	nm, _ = m.Update(storesLoadedMsg{gen: m.storesGen, stores: []openfga.Store{{ID: "sbx-store", Name: "github"}}})
	m = nm.(Model)
	if len(m.wb.results) != 1 {
		t.Fatal("stores reload wiped test results")
	}

	on, _ = config.Load()
	for _, n := range on.ProfileNames() {
		if n == "playground" {
			t.Fatal("playground profile persisted")
		}
	}
	raw, _ := os.ReadFile(os.Getenv("OPENFGA_CONFIG"))
	for _, bad := range []string{sandboxEndpoint, "sbx-store", "playground"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("config contains %q:\n%s", bad, raw)
		}
	}
}

func TestSandboxModel_NameCollisionWithRealProfile(t *testing.T) {
	configtest.Isolate(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Set("playground", config.Profile{APIURL: "http://127.0.0.1:1"})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	a := cli.New(log.New(io.Discard), cfg, "test")
	cl, _ := openfga.NewClient(sandboxEndpoint)
	m := newSandboxModel(context.Background(), a, cl, SandboxOptions{Endpoint: sandboxEndpoint})

	if m.profile == "playground" || m.profile != m.ephemeralProfile {
		t.Fatalf("sandbox profile = %q (ephemeral %q), want a distinct name", m.profile, m.ephemeralProfile)
	}
	ids := map[string]int{}
	for _, it := range m.profilesList.Model.Items() {
		ids[it.(uilist.Item).ID]++
	}
	if ids["playground"] != 1 || ids[m.ephemeralProfile] != 1 {
		t.Fatalf("profile row IDs = %v, want one real and one sandbox row", ids)
	}

	m.switchProfile("playground")
	if m.profile != "playground" || a.Config.Active != "playground" {
		t.Fatalf("profile=%q active=%q, want the real playground profile", m.profile, a.Config.Active)
	}
	m.switchProfile(m.ephemeralProfile)
	if m.profile != m.ephemeralProfile {
		t.Fatalf("profile = %q, want sandbox back", m.profile)
	}
}

func TestSandboxClient_RecordsCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"stores":[]}`)
	}))
	t.Cleanup(srv.Close)
	configtest.Isolate(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	a := cli.New(log.New(io.Discard), cfg, "test")
	rec := apilog.NewRecorder(10)
	cl, err := newSandboxClient(a, srv.URL, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Stores.List(context.Background(), nil); err != nil {
		t.Fatalf("list stores: %v", err)
	}
	if rec.Len() != 1 {
		t.Fatalf("recorded %d calls, want 1", rec.Len())
	}
}

func TestSandboxModel_NameCollisionChain(t *testing.T) {
	configtest.Isolate(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Set("playground", config.Profile{APIURL: "http://127.0.0.1:1"})
	cfg.Set("playground (sandbox)", config.Profile{APIURL: "http://127.0.0.1:2"})
	a := cli.New(log.New(io.Discard), cfg, "test")
	cl, _ := openfga.NewClient(sandboxEndpoint)
	m := newSandboxModel(context.Background(), a, cl, SandboxOptions{Endpoint: sandboxEndpoint})

	if m.ephemeralProfile != "playground (sandbox 2)" {
		t.Fatalf("sandbox profile = %q, want %q", m.ephemeralProfile, "playground (sandbox 2)")
	}
	ids := map[string]int{}
	for _, it := range m.profilesList.Model.Items() {
		ids[it.(uilist.Item).ID]++
	}
	for _, id := range []string{"playground", "playground (sandbox)", m.ephemeralProfile} {
		if ids[id] != 1 {
			t.Fatalf("profile row IDs = %v, want exactly one %q", ids, id)
		}
	}
	m.switchProfile("playground (sandbox)")
	if a.Config.Active != "playground (sandbox)" {
		t.Fatalf("active = %q, want the real %q profile", a.Config.Active, "playground (sandbox)")
	}
}

func TestSandboxModel_EditedWorkspaceSurvivesStoreSwitch(t *testing.T) {
	m, _, wss := newTestSandbox(t)
	root := copyDocsWorkspace(t)
	ws, err := modeltest.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	wss["github"] = ws // the model shares this map
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	before := len(m.wb.files)

	path := filepath.Join(root, "tests", "extra.test.yaml")
	body := "tests:\n  - name: extra\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	nm, _ := m.Update(testFileEditedMsg{path: path})
	m = nm.(Model)
	if len(m.wb.files) != before+1 {
		t.Fatalf("files after edit = %d, want %d", len(m.wb.files), before+1)
	}

	m.selectStore(openfga.Store{ID: "s2", Name: "gdrive"})
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	if len(m.wb.files) != before+1 {
		t.Fatalf("files after switching back = %d, want %d (startup snapshot restored)", len(m.wb.files), before+1)
	}
}

func TestSandboxModel_StaleRunDroppedAfterStoreSwitch(t *testing.T) {
	m, _, _ := newTestSandbox(t)
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	m.runSuite("")
	staleGen := m.wb.runGen
	m.selectStore(openfga.Store{ID: "s2", Name: "gdrive"})
	if m.wb.running {
		t.Fatal("running still set after the workspace changed")
	}

	stale := &modeltest.Results{
		Tests:    []modeltest.TestResult{{Name: "github-test"}},
		Coverage: &modeltest.Coverage{},
	}
	loads := m.pendingLoads
	nm, _ := m.Update(testsRanMsg{gen: staleGen, results: stale})
	m = nm.(Model)
	if len(m.wb.results) != 0 || m.wb.coverage != nil {
		t.Fatalf("stale run applied: results=%d coverage=%v", len(m.wb.results), m.wb.coverage)
	}
	if m.pendingLoads != loads-1 {
		t.Fatalf("pendingLoads = %d, want %d (a stale run must still end its load)", m.pendingLoads, loads-1)
	}
}

func TestSandboxModel_StoreSwitchClearsCoverage(t *testing.T) {
	m, _, _ := newTestSandbox(t)
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	m.wb.coverage = &modeltest.Coverage{}
	m.wb.showCoverage = true
	m.wb.coverageErr = "x"
	m.wb.covScroll, m.wb.detailScroll = 3, 2
	m.selectStore(openfga.Store{ID: "s2", Name: "gdrive"})
	if m.wb.coverage != nil || m.wb.showCoverage || m.wb.coverageErr != "" || m.wb.covScroll != 0 || m.wb.detailScroll != 0 {
		t.Fatalf("coverage state survived the switch: %+v", m.wb)
	}
}

func TestSandboxModel_AddProfileRejectsSandboxName(t *testing.T) {
	m0, a, _ := newTestSandbox(t)
	var m tea.Model = m0
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(key("1"))     // Profiles
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("a"))     // add-profile form
	if m.(Model).formKind != formAddProfile {
		t.Fatalf("a should open the add-profile form; got kind=%d", m.(Model).formKind)
	}
	for _, r := range "playground" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("enter")) // -> api_url field
	for _, r := range "http://example:9090" {
		m, _ = m.Update(key(string(r)))
	}
	for i := 0; i < 6 && m.(Model).formErr == ""; i++ {
		m, _ = m.Update(key("enter"))
	}
	mm := m.(Model)
	if !strings.Contains(mm.formErr, "in use by this session") {
		t.Fatalf("formErr = %q, want the sandbox-name rejection", mm.formErr)
	}
	if _, ok := a.Config.Get("playground"); ok {
		t.Fatal("a real profile named after the sandbox was created")
	}
}

// Sample test files live in the session's scratch dir, so editing one in the
// sandbox must say the change is session-only rather than silently drop it at quit.
func TestSandboxModel_TestEditNoteOnlyOnSandboxProfile(t *testing.T) {
	m, _, _ := newTestSandbox(t)
	if note := m.sandboxTestEditNote(); !strings.Contains(note, "not kept after quit") {
		t.Fatalf("sandbox note = %q, want a session-only warning", note)
	}
	m.profile = "real"
	if note := m.sandboxTestEditNote(); note != "" {
		t.Fatalf("note on a real profile = %q, want none", note)
	}
	if note := (&Model{}).sandboxTestEditNote(); note != "" {
		t.Fatalf("note outside the sandbox = %q, want none", note)
	}
}

// Deleting the sandbox's selected store must also forget it as the sandbox
// profile's remembered store, or switching back to the sandbox would aim every
// store-scoped request at the deleted store.
func TestSandboxModel_DeletedStoreNotRestoredOnSwitchBack(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		m, _, _ := newTestSandbox(t)
		m.selectStore(openfga.Store{ID: "s1", Name: "github"})
		origin := m.mutationOrigin("s1", "", m.storeDeleteGen)
		if inFlight {
			// The user left the sandbox while the delete was in flight, so the
			// reply is stale for everything except the remembered store.
			m.profile = "real"
			m.storeDeleteGen++
		}
		nm, _ := m.Update(storeDeletedMsg{origin: origin, id: "s1"})
		m = nm.(Model)
		if m.ephemeralStoreID != "" {
			t.Fatalf("inFlight=%v: ephemeralStoreID = %q after its store was deleted", inFlight, m.ephemeralStoreID)
		}
		m.switchToEphemeral()
		if m.storeID == "s1" {
			t.Fatalf("inFlight=%v: switching back restored the deleted store", inFlight)
		}
	}
}

// A failed delete leaves the store in place, so it stays remembered.
func TestSandboxModel_FailedDeleteKeepsRememberedStore(t *testing.T) {
	m, _, _ := newTestSandbox(t)
	m.selectStore(openfga.Store{ID: "s1", Name: "github"})
	nm, _ := m.Update(storeDeletedMsg{origin: m.mutationOrigin("s1", "", m.storeDeleteGen), id: "s1", err: errors.New("boom")})
	if got := nm.(Model).ephemeralStoreID; got != "s1" {
		t.Fatalf("ephemeralStoreID = %q after a failed delete, want s1", got)
	}
}
