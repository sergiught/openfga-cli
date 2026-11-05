package playground

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
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
	m := newSandboxModel(context.Background(), a, SandboxOptions{Client: cl, Endpoint: sandboxEndpoint, Workspaces: wss})
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
