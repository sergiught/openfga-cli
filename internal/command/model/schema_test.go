package model

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each model command with --json and checks
// the document against the published schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user, user:*] or owner\n    define owner: [user]\n"
	m := openfga.AuthorizationModel{
		ID:            "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "document", Relations: map[string]openfga.Userset{
				"owner":  {This: &openfga.DirectUserset{}},
				"viewer": {Union: &openfga.Usersets{Child: []openfga.Userset{{This: &openfga.DirectUserset{}}, {ComputedUserset: &openfga.ObjectRelation{Relation: "owner"}}}}},
			}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"authorization_model_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`))
		case strings.HasSuffix(r.URL.Path, "/authorization-models"):
			_ = json.NewEncoder(w).Encode(openfga.ListAuthorizationModelsResponse{AuthorizationModels: []openfga.AuthorizationModel{m}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_model": m})
		}
	}))
	defer srv.Close()

	modelFile := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(modelFile, []byte(dsl), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		sub  string
		args []string
	}{
		{"ofga model write", "write", []string{"--file", modelFile}},
		{"ofga model write", "write", []string{"--file", modelFile, "--dry-run"}},
		{"ofga model list", "list", nil},
		{"ofga model get", "get", []string{m.ID}},
		{"ofga model latest", "latest", nil},
		{"ofga model graph", "graph", nil},
		{"ofga model test", "test", []string{"--file", passingWorkspace}},
		{"ofga model test", "test", []string{"--file", passingWorkspace, "--coverage"}},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := runTestCLI(t, srv.URL)
			c := New(a)
			cmd := map[string]func() *cobra.Command{
				"write": c.writeCmd, "list": c.listCmd, "get": c.getCmd, "latest": c.latestCmd,
				"graph": c.graphCmd, "test": c.testCmd,
			}[tc.sub]()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}

	t.Run("ofga model test failing run", func(t *testing.T) {
		a := runTestCLI(t, srv.URL)
		cmd := New(a).testCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs([]string{"--file", writeFailingWorkspace(t)})
		if err := cmd.Execute(); err == nil {
			t.Fatal("failing workspace should fail the command")
		}
		outputschematest.Validate(t, "ofga model test", out.Bytes())
	})
}

func runTestCLI(t *testing.T, apiURL string) *cli.CLI {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	a := cli.New(log.New(io.Discard), cfg, "test")
	a.JSON = true
	return a
}
