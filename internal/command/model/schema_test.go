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
		{"ofga model test schema", "schema", nil},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := runTestCLI(t, srv.URL)
			c := New(a)
			cmd := map[string]func() *cobra.Command{
				"write": c.writeCmd, "list": c.listCmd, "get": c.getCmd, "latest": c.latestCmd,
				"graph": c.graphCmd, "test": c.testCmd, "schema": c.testSchemaCmd,
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

// TestSchemaAcceptsNullAssertionsFromUnexecutableTest: a test that cannot run
// (here: an assertion on an undefined relation) is reported with
// "assertions": null, and the published schema must accept that.
func TestSchemaAcceptsNullAssertionsFromUnexecutableTest(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("tests/a.test.yaml", "tests:\n  - name: bad-relation\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {nosuchrel: true}\n")

	cmd := New(runTestCLI(t, "http://127.0.0.1:1")).testCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--file", filepath.Join(dir, "ofga.yaml")})
	_ = cmd.Execute() // the run fails; stdout still carries the result
	if !strings.Contains(out.String(), `"assertions": null`) {
		t.Fatalf("workspace did not produce a null assertions list:\n%s", out.String())
	}
	outputschematest.Validate(t, "ofga model test", out.Bytes())
}

// TestSchemaValidatesTestInitOutput runs `model test init --json` in a temp
// dir and checks stdout against the schema; human lines must stay off stdout.
func TestSchemaValidatesTestInitOutput(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	a := runTestCLI(t, "http://127.0.0.1:1")
	cmd := New(a).testInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	outputschematest.Validate(t, "ofga model test init", out.Bytes())
	var got struct {
		Dir     string   `json:"dir"`
		Created []string `json:"created"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"ofga.yaml", "workspace.schema.json", "model.fga", "fixtures/example.yaml", "tests/example.test.yaml"}
	if got.Dir != dir || strings.Join(got.Created, ",") != strings.Join(want, ",") {
		t.Errorf("got %+v, want dir %q created %v", got, dir, want)
	}
}
