package base

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func agentMockServer(t *testing.T) []string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/stores":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stores": []map[string]any{
					{"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": "demo",
						"created_at": "2023-01-01T00:00:00Z", "updated_at": "2023-01-01T00:00:00Z"},
				},
				"continuation_token": "",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/stores":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": "x",
				"created_at": "2023-01-01T00:00:00Z", "updated_at": "2023-01-01T00:00:00Z",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return []string{"OPENFGA_API_URL=" + srv.URL}
}

func oneLine(s string) bool {
	return strings.Count(s, "\n") == 1 && strings.HasSuffix(s, "\n")
}

func TestAgentModeDefaultsToCompactJSON(t *testing.T) {
	env := append(agentMockServer(t), "CLAUDECODE=1")
	out, errb, code := runOfga(t, t.TempDir(), "", env, "stores", "list")
	if code != 0 || !oneLine(out) || !json.Valid([]byte(out)) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
}

func TestAgentModeExplicitFormatWins(t *testing.T) {
	env := append(agentMockServer(t), "CLAUDECODE=1")
	home := t.TempDir()
	out, _, _ := runOfga(t, home, "", env, "stores", "list", "-o", "table")
	if json.Valid([]byte(out)) || !strings.Contains(out, "demo") {
		t.Errorf("-o table should be a table: %q", out)
	}
	out, _, _ = runOfga(t, home, "", env, "stores", "list", "--plain")
	if !strings.Contains(out, "\t") || json.Valid([]byte(out)) {
		t.Errorf("--plain should be tab-separated: %q", out)
	}
	out, _, _ = runOfga(t, home, "", env, "stores", "list", "--yaml")
	if json.Valid([]byte(out)) || !strings.Contains(out, "name: demo") {
		t.Errorf("--yaml should be YAML: %q", out)
	}
}

func TestAgentModeJSONIsCompactEvenWhenExplicit(t *testing.T) {
	env := append(agentMockServer(t), "CLAUDECODE=1")
	out, _, code := runOfga(t, t.TempDir(), "", env, "stores", "list", "--json")
	if code != 0 || !oneLine(out) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestAgentModeDropsSuccessKeepsWarnings(t *testing.T) {
	env := append(agentMockServer(t), "CLAUDECODE=1")
	home := t.TempDir()
	_, errb, code := runOfga(t, home, "", env, "stores", "create", "x")
	if code != 0 || strings.Contains(errb, "created store") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	// --use against a profile that does not exist is a Warnf path.
	_, errb, _ = runOfga(t, home, "", env, "stores", "create", "x", "--use", "--profile", "ghost")
	if !strings.Contains(errb, "not found") {
		t.Fatalf("warning must survive agent mode: %q", errb)
	}
}

func TestAgentModeImpliesNoInput(t *testing.T) {
	configtest.Isolate(t)
	t.Setenv("CLAUDECODE", "1")
	c := New(cli.New(log.New(io.Discard), config.New(), "test"))
	root := c.Command()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, err := root.Flags().GetBool("no-input"); err != nil || !got {
		t.Fatalf("no-input = %v, %v", got, err)
	}
	if !c.cli.NoInput || !c.cli.NoColor || !c.cli.Agent.Enabled {
		t.Fatalf("agent effects not applied: %+v", c.cli)
	}
	if !c.cli.JSON {
		t.Fatal("default format must flow through resolveOutput into c.cli.JSON")
	}
}

func TestAgentModeOptOut(t *testing.T) {
	env := append(agentMockServer(t), "CLAUDECODE=1", "OPENFGA_AGENT_MODE=0")
	out, _, code := runOfga(t, t.TempDir(), "", env, "stores", "list")
	if code != 0 || json.Valid([]byte(out)) || !strings.Contains(out, "demo") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestVersionReportsAgentMode(t *testing.T) {
	out, errb, code := runOfga(t, t.TempDir(), "", []string{"CLAUDECODE=1"}, "version", "--json")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errb)
	}
	var v struct {
		AgentMode struct {
			Enabled bool   `json:"enabled"`
			Reason  string `json:"reason"`
		} `json:"agent_mode"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if !v.AgentMode.Enabled || v.AgentMode.Reason != "CLAUDECODE=1" {
		t.Fatalf("agent_mode = %+v", v.AgentMode)
	}
	out, _, _ = runOfga(t, t.TempDir(), "", nil, "version", "--plain")
	if !strings.Contains(out, "agent mode") || !strings.Contains(out, "off") {
		t.Fatalf("plain version lacks agent mode row: %q", out)
	}
}

func TestPlaygroundRefusesInAgentMode(t *testing.T) {
	configtest.Isolate(t)
	t.Setenv("CLAUDECODE", "1")
	for _, args := range [][]string{{"playground"}, {}} {
		c := New(cli.New(log.New(io.Discard), config.New(), "test"))
		root := c.Command()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SilenceUsage, root.SilenceErrors = true, true
		root.SetArgs(args)
		err := root.Execute()
		if err == nil || clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "agent mode is on") {
			t.Fatalf("args=%v code=%d err=%v", args, clierr.Code(err), err)
		}
	}
}
