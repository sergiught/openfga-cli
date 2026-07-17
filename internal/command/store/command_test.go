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
