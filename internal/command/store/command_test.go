package store

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
)

// newStoreCLI builds a CLI pointed at apiURL that renders human/plain output.
func newStoreCLI(t *testing.T, apiURL string) *cli.CLI {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL})
	return cli.New(log.New(io.Discard), cfg, "test")
}

func TestListPlainTimestampsAreRFC3339(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ListStoresResponse{
			Stores: []openfga.Store{{
				ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				Name:      "acme",
				CreatedAt: time.Unix(1600000000, 0).UTC(),
			}},
		})
	}))
	defer srv.Close()

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newStoreCLI(t, srv.URL)).listCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// CREATED is the 3rd tab-separated column.
	fields := strings.Split(strings.TrimSpace(out.String()), "\t")
	if len(fields) != 3 {
		t.Fatalf("row = %q, want 3 tab-separated columns", out.String())
	}
	if _, err := time.Parse(time.RFC3339, fields[2]); err != nil {
		t.Errorf("CREATED = %q, not RFC3339: %v", fields[2], err)
	}
}

func TestGetPlainTimestampsAreRFC3339(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.Store{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
			Name:      "acme",
			CreatedAt: time.Unix(1600000000, 0).UTC(),
			UpdatedAt: time.Unix(1600000100, 0).UTC(),
		})
	}))
	defer srv.Close()

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newStoreCLI(t, srv.URL)).getCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// KeyValues plain output is "key\tvalue" per line.
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if kv := strings.SplitN(line, "\t", 2); len(kv) == 2 {
			values[kv[0]] = kv[1]
		}
	}
	for _, key := range []string{"created_at", "updated_at"} {
		if _, err := time.Parse(time.RFC3339, values[key]); err != nil {
			t.Errorf("%s = %q, not RFC3339: %v", key, values[key], err)
		}
	}
}

func TestListRejectsNegativeMaxBeforeClientCreation(t *testing.T) {
	cmd := (&Command{}).listCmd()
	cmd.SetArgs([]string{"--max-results=-1"})
	if err := cmd.Execute(); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestStoreDryRunShorthand(t *testing.T) {
	c := &Command{}
	for _, cmd := range []*cobra.Command{c.createCmd(), c.deleteCmd()} {
		if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
			t.Errorf("%s --dry-run shorthand = %q, want n", cmd.Name(), got)
		}
	}
}

// storeCreateServer returns a test server that answers Stores.Create with a
// fixed store ID.
func storeCreateServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.Store{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
			Name:      "acme",
			CreatedAt: time.Unix(1600000000, 0).UTC(),
			UpdatedAt: time.Unix(1600000000, 0).UTC(),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCreateUseHonorsEnvProfileOverride reproduces CLI-74/CLI-83: with
// OPENFGA_PROFILE set, `stores create --use` must save the new store ID into
// the env-selected profile, not the file-active one.
func TestCreateUseHonorsEnvProfileOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := storeCreateServer(t)

	cfg := config.New()
	cfg.Active = "dev"
	cfg.Set("dev", config.Profile{APIURL: srv.URL})
	cfg.Set("prod", config.Profile{APIURL: srv.URL})
	c := cli.New(log.New(io.Discard), cfg, "test")

	t.Setenv("OPENFGA_PROFILE", "prod")

	cmd := New(c).createCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"acme", "--use"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if p, _ := cfg.Get("prod"); p.StoreID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("prod profile StoreID = %q, want the new store ID", p.StoreID)
	}
	if p, _ := cfg.Get("dev"); p.StoreID != "" {
		t.Errorf("dev (file-active) profile StoreID = %q, want empty; env override was ignored", p.StoreID)
	}
}

// TestCreateUseFlagBeatsEnvProfile checks --profile takes precedence over
// OPENFGA_PROFILE, matching config.ActiveName's documented precedence.
func TestCreateUseFlagBeatsEnvProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := storeCreateServer(t)

	cfg := config.New()
	cfg.Active = "dev"
	cfg.Set("dev", config.Profile{APIURL: srv.URL})
	cfg.Set("prod", config.Profile{APIURL: srv.URL})
	cfg.Set("staging", config.Profile{APIURL: srv.URL})
	c := cli.New(log.New(io.Discard), cfg, "test")
	c.Overrides.Profile = "staging"

	t.Setenv("OPENFGA_PROFILE", "prod")

	cmd := New(c).createCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"acme", "--use"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if p, _ := cfg.Get("staging"); p.StoreID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("staging profile StoreID = %q, want the new store ID", p.StoreID)
	}
	if p, _ := cfg.Get("prod"); p.StoreID != "" {
		t.Errorf("prod profile StoreID = %q, want empty; --profile should beat OPENFGA_PROFILE", p.StoreID)
	}
}

// missingProfileScenario builds a CLI whose resolved profile ("dev") is
// removed from the config while the create request is in flight, simulating
// another process (or the TUI) changing the profile set between this
// command's connection resolve and its post-create save. This is the only
// way a profile the command already resolved against can be gone by the time
// it tries to persist the new store ID.
func missingProfileScenario(t *testing.T) *cli.CLI {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config.New()
	cfg.Active = "dev"
	cfg.Set("dev", config.Profile{})

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delete(cfg.Profiles, "dev")
		_ = json.NewEncoder(w).Encode(openfga.Store{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
			Name:      "acme",
			CreatedAt: time.Unix(1600000000, 0).UTC(),
			UpdatedAt: time.Unix(1600000000, 0).UTC(),
		})
	}))
	t.Cleanup(srv.Close)
	cfg.Profiles["dev"] = config.Profile{APIURL: srv.URL}

	return cli.New(log.New(io.Discard), cfg, "test")
}

// TestCreateUseMissingProfileWarnsAndReportsNotSaved covers the resolved
// profile not existing at save time: the command must not claim the store ID
// was saved, must warn on stderr, and machine output must report the save
// outcome.
func TestCreateUseMissingProfileWarnsAndReportsNotSaved(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		c := missingProfileScenario(t)
		cmd := New(c).createCmd()
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{"acme", "--use"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(errOut.String(), `profile "dev" not found`) || !strings.Contains(errOut.String(), "not saved") {
			t.Errorf("stderr = %q, want a warning that profile %q was not found and not saved", errOut.String(), "dev")
		}
		if strings.Contains(errOut.String(), "set as") {
			t.Errorf("stderr = %q, must not claim the store was set as a profile's store", errOut.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		c := missingProfileScenario(t)
		c.JSON = true
		cmd := New(c).createCmd()
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{"acme", "--use"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal: %v; out=%s", err, out.String())
		}
		saved, ok := got["saved_to_profile"]
		if !ok {
			t.Fatalf("json output missing %q field: %s", "saved_to_profile", out.String())
		}
		if saved != "" {
			t.Errorf("saved_to_profile = %v, want empty (profile does not exist)", saved)
		}
	})
}
