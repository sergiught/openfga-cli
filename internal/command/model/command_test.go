package model

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestModelInputToJSON(t *testing.T) {
	dsl := "model\n  schema 1.1\ntype user\n"
	jsonModel := `{"schema_version":"1.1","type_definitions":[{"type":"user"}]}`

	t.Run("json extension passes through unchanged", func(t *testing.T) {
		out, err := modelInputToJSON([]byte(jsonModel), "model.json")
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != jsonModel {
			t.Fatalf("got %q, want unchanged JSON", out)
		}
	})

	t.Run("fga extension is transformed to JSON", func(t *testing.T) {
		out, err := modelInputToJSON([]byte(dsl), "model.fga")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `"schema_version"`) || !strings.Contains(string(out), `"user"`) {
			t.Fatalf("expected transformed JSON, got %q", out)
		}
	})

	t.Run("stdin JSON is sniffed and passed through", func(t *testing.T) {
		in := "  " + jsonModel
		out, err := modelInputToJSON([]byte(in), "-")
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != in {
			t.Fatalf("got %q, want unchanged JSON", out)
		}
	})

	t.Run("stdin DSL is sniffed and transformed", func(t *testing.T) {
		out, err := modelInputToJSON([]byte(dsl), "-")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `"type_definitions"`) {
			t.Fatalf("expected transformed JSON, got %q", out)
		}
	})
}

func TestListRejectsNegativeMaxBeforeClientCreation(t *testing.T) {
	cmd := (&Command{}).listCmd()
	cmd.SetArgs([]string{"--limit=-1"})
	if err := cmd.Execute(); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestModelDryRunShorthand(t *testing.T) {
	cmd := (&Command{}).writeCmd()
	if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
		t.Fatalf("--dry-run shorthand = %q, want n", got)
	}
}

func TestGetAndLatestDefaultToDSL(t *testing.T) {
	model := openfga.AuthorizationModel{
		ID:              "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		SchemaVersion:   "1.1",
		TypeDefinitions: []openfga.TypeDefinition{{Type: "user"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authorization-models") {
			_ = json.NewEncoder(w).Encode(openfga.ListAuthorizationModelsResponse{
				AuthorizationModels: []openfga.AuthorizationModel{model},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_model": model})
	}))
	defer srv.Close()

	run := func(t *testing.T, sub string, mutate func(*cli.CLI)) string {
		t.Helper()
		cfg := config.New()
		cfg.Set("default", config.Profile{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		a := cli.New(log.New(io.Discard), cfg, "test")
		if mutate != nil {
			mutate(a)
		}
		c := New(a)
		var cmd = c.latestCmd()
		var args []string
		if sub == "get" {
			cmd = c.getCmd()
			args = []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV"}
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	for _, sub := range []string{"get", "latest"} {
		t.Run(sub+" defaults to DSL", func(t *testing.T) {
			got := run(t, sub, nil)
			if !strings.Contains(got, "type user") || !strings.Contains(got, "schema 1.1") {
				t.Fatalf("default output is not DSL:\n%s", got)
			}
			if strings.Contains(got, "{") {
				t.Fatalf("default output leaked JSON:\n%s", got)
			}
		})

		t.Run(sub+" --json emits JSON", func(t *testing.T) {
			got := run(t, sub, func(a *cli.CLI) { a.JSON = true })
			if !strings.Contains(got, `"type_definitions"`) {
				t.Fatalf("--json output is not JSON:\n%s", got)
			}
		})

		t.Run(sub+" --plain emits a metadata summary", func(t *testing.T) {
			got := run(t, sub, func(a *cli.CLI) { a.Plain = true })
			if !strings.Contains(got, "model_id") || !strings.Contains(got, "schema") {
				t.Fatalf("--plain output is not the summary:\n%s", got)
			}
		})
	}
}

func TestListPrintsCountFooterOnStderr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ListAuthorizationModelsResponse{
			AuthorizationModels: []openfga.AuthorizationModel{
				{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SchemaVersion: "1.1"},
				{ID: "01ARZ3NDEKTSV4RRFFQ69G5FBW", SchemaVersion: "1.1"},
			},
		})
	}))
	defer srv.Close()

	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	cmd := New(cli.New(log.New(io.Discard), cfg, "test")).listCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "2 model(s)") {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), "2 model(s)")
	}
	if strings.Contains(out.String(), "model(s)") {
		t.Errorf("count footer leaked onto stdout:\n%s", out.String())
	}
}
