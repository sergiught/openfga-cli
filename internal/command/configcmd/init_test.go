package configcmd

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/agentmode"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func TestInitModelIDFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENFGA_CONFIG", filepath.Join(dir, "config.toml"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := cli.New(log.New(io.Discard), cfg, "test")
	cmd := NewInit(c)
	cmd.SetArgs([]string{"prod", "--api-url", "https://api.example", "--store-id", "01STORE", "--model-id", "01MODEL", "--force"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reloaded.Get("prod")
	if !ok {
		t.Fatal("init did not create the profile")
	}
	if p.StoreID != "01STORE" || p.ModelID != "01MODEL" {
		t.Fatalf("persisted store/model wrong: store=%q model=%q", p.StoreID, p.ModelID)
	}
}

func TestInitRefusesWithoutFlagsInAgentMode(t *testing.T) {
	configtest.Isolate(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := cli.New(log.New(io.Discard), cfg, "test")
	c.Agent = agentmode.State{Enabled: true, Reason: "CLAUDECODE=1"}
	cmd := NewInit(c)
	cmd.SetArgs(nil)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err = cmd.Execute()
	if err == nil || clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "agent mode is on") {
		t.Fatalf("code=%d err=%v", clierr.Code(err), err)
	}
}
