package mapping

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"charm.land/log/v2"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/agentmode"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

const needsTerminal = "ofga mappings opens an interactive workspace and needs a terminal"

func TestTheWorkspaceNeedsATerminal(t *testing.T) {
	configtest.Isolate(t)
	for name, set := range map[string]func(*cli.CLI){
		"no-input": func(c *cli.CLI) { c.NoInput = true },
		"json":     func(c *cli.CLI) { c.JSON = true },
		"yaml":     func(c *cli.CLI) { c.YAML = true },
		"non-tty":  func(*cli.CLI) {},
	} {
		t.Run(name, func(t *testing.T) {
			c := cli.New(log.New(io.Discard), config.New(), "test")
			set(c)
			_, err := run(t, c)
			if err == nil || !strings.Contains(err.Error(), needsTerminal) || clierr.Code(err) != clierr.CodeUsage {
				t.Fatalf("code=%d err=%v", clierr.Code(err), err)
			}
		})
	}
}

func TestATypoIsAnUnknownCommand(t *testing.T) {
	configtest.Isolate(t)
	t.Chdir(t.TempDir())
	_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "valdate")
	if err == nil || clierr.Code(err) != clierr.CodeUsage ||
		!strings.Contains(err.Error(), `unknown command "valdate" for "mappings"`) ||
		!strings.Contains(err.Error(), "Did you mean this?") || !strings.Contains(err.Error(), "validate") {
		t.Fatalf("code=%d err=%v", clierr.Code(err), err)
	}
}

func TestAnExistingFileArgumentIsNotATypo(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	// "valdate" is one edit from "validate", so it is a typo unless the file
	// exists. It then reaches the terminal check instead.
	if err := os.WriteFile(filepath.Join(dir, "valdate"), []byte(validMapping), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "valdate")
	if err == nil || !strings.Contains(err.Error(), needsTerminal) {
		t.Fatalf("err = %v", err)
	}
}

// The workspace edits one file: a second file argument is refused before
// anything opens, rather than silently ignored.
func TestTheWorkspaceRefusesTwoFileArguments(t *testing.T) {
	configtest.Isolate(t)
	t.Chdir(t.TempDir())
	_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "a.yaml", "b.yaml")
	if err == nil || !strings.Contains(err.Error(), "accepts at most 1 arg(s), received 2") || clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code=%d err=%v, want the argument count refused as a usage error", clierr.Code(err), err)
	}
}

func TestHelpDescribesTheWorkspace(t *testing.T) {
	configtest.Isolate(t)
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "--help")
	if err != nil || !strings.Contains(out, "interactive workspace") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestStoreModelHonoursModelID(t *testing.T) {
	configtest.Isolate(t)
	for name, tc := range map[string]struct{ modelID, want string }{
		"model id": {"01ARZ3NDEKTSV4RRFFQ69G5FAW", "/authorization-models/01ARZ3NDEKTSV4RRFFQ69G5FAW"},
		"latest":   {"", "/authorization-models"},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				path = r.URL.Path
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"authorization_model":{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","schema_version":"1.1","type_definitions":[{"type":"user"}]},"authorization_models":[{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","schema_version":"1.1","type_definitions":[{"type":"user"}]}]}`)
			}))
			defer srv.Close()
			cfg := config.New()
			cfg.Set("default", config.Profile{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ModelID: tc.modelID})
			c := New(cli.New(log.New(io.Discard), cfg, "test"))
			m, err := c.storeModel(t.Context())
			mu.Lock()
			defer mu.Unlock()
			if err != nil || m.ID != "01ARZ3NDEKTSV4RRFFQ69G5FAW" || !strings.HasSuffix(path, tc.want) {
				t.Fatalf("path=%q m=%v err=%v", path, m, err)
			}
		})
	}
}

func TestStoreOptionOnlyWithAStore(t *testing.T) {
	configtest.Isolate(t)
	none := New(cli.New(log.New(io.Discard), config.New(), "test")).workspaceOptions(&cobra.Command{}, nil)
	if none.Store != "" || none.LoadStoreModel != nil {
		t.Fatalf("no store: %+v", none)
	}
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "http://localhost:8080", StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	got := New(cli.New(log.New(io.Discard), cfg, "v1")).workspaceOptions(&cobra.Command{}, []string{"m.yaml"})
	if got.Store != "profile default, store 01ARZ3NDEKTSV4RRFFQ69G5FAV" || got.LoadStoreModel == nil ||
		got.Path != "m.yaml" || got.Version != "v1" || filepath.Base(got.RecentFile) != "mappings-recent.json" {
		t.Fatalf("with store: %+v", got)
	}
}

func TestInteractiveRejectsNonFileStdin(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewBufferString(""))
	if interactive(cmd, cli.New(log.New(io.Discard), config.New(), "test")) {
		t.Fatal("a buffer stdin should not be eligible")
	}
}

func TestWorkspaceOptionsCarryTheStore(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: fs.srv.URL, StoreID: applyStoreID})
	cmd := &cobra.Command{}
	cmd.Flags().String("store-id", "", "")
	if err := cmd.Flags().Set("store-id", applyStoreID); err != nil {
		t.Fatal(err)
	}
	got := New(cli.New(log.New(io.Discard), cfg, "test")).workspaceOptions(cmd, nil).Apply
	if got == nil {
		t.Fatal("Apply is nil with a store set")
	}
	if got.StoreID != applyStoreID || got.Profile != "default" || got.Name(t.Context()) != "acme" {
		t.Fatalf("target = %+v", got)
	}
	if got.Reader == nil || got.Writer == nil || got.Hints == nil {
		t.Fatalf("reader/writer/hints missing: %+v", got)
	}
	want := "ofga mappings apply m.yaml --events EVENTS_FILE --from-event 3 --store-id " + applyStoreID
	if line := got.Resume("m.yaml", "", 3); line != want {
		t.Fatalf("resume = %q, want %q", line, want)
	}
}

func TestWorkspaceOptionsWithoutAStoreHaveNoApply(t *testing.T) {
	configtest.Isolate(t)
	opts := New(cli.New(log.New(io.Discard), config.New(), "test")).workspaceOptions(&cobra.Command{}, nil)
	if opts.Apply != nil {
		t.Fatalf("Apply = %+v, want nil", opts.Apply)
	}
}

func TestWorkspaceOptionsSayWhyAStoreCannotBeUsed(t *testing.T) {
	configtest.Isolate(t)
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "http://localhost:8080", StoreID: "my-store"})
	opts := New(cli.New(log.New(io.Discard), cfg, "test")).workspaceOptions(&cobra.Command{}, nil)
	if opts.Apply != nil || opts.Store == "" {
		t.Fatalf("Apply = %+v, Store = %q; want no target beside the configured store", opts.Apply, opts.Store)
	}
	if !strings.Contains(opts.ApplyErr, `store ID "my-store"`) || !strings.Contains(opts.ApplyErr, "must be a ULID") {
		t.Fatalf("ApplyErr = %q, want it to name the bad store ID", opts.ApplyErr)
	}
	// With no store at all there is nothing to explain.
	none := New(cli.New(log.New(io.Discard), config.New(), "test")).workspaceOptions(&cobra.Command{}, nil)
	if none.ApplyErr != "" {
		t.Fatalf("ApplyErr = %q with no store", none.ApplyErr)
	}
}

func TestWorkspaceOptionsSayWhyTheConfigDoesNotResolve(t *testing.T) {
	configtest.Isolate(t)
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.Overrides.Profile = "typo"
	opts := New(c).workspaceOptions(&cobra.Command{}, nil)
	if opts.Apply != nil || opts.Store != "" || opts.LoadStoreModel != nil {
		t.Fatalf("a config that does not resolve gives no store: %+v", opts)
	}
	if !strings.Contains(opts.ApplyErr, "typo") {
		t.Fatalf("ApplyErr = %q, want it to name the unknown profile", opts.ApplyErr)
	}
}

func TestTheWorkspaceRefusesInAgentMode(t *testing.T) {
	configtest.Isolate(t)
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.Agent = agentmode.State{Enabled: true, Reason: "CLAUDECODE=1"}
	_, err := run(t, c)
	if err == nil || clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "agent mode is on") {
		t.Fatalf("code=%d err=%v", clierr.Code(err), err)
	}
}
