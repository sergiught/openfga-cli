package configcmd

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
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
