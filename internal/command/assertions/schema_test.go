package assertions

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestSchemasValidateRealOutput runs each assertions command with --json and
// checks the document against the published schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	const modelID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/check"):
			_, _ = w.Write([]byte(`{"allowed":true}`))
		default:
			_ = json.NewEncoder(w).Encode(openfga.ReadAssertionsResponse{
				AuthorizationModelID: modelID,
				Assertions: []openfga.Assertion{
					{TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"}, Expectation: true},
					{TupleKey: openfga.CheckRequestTupleKey{User: "user:bob", Relation: "editor", Object: "doc:2"}, Expectation: false},
				},
			})
		}
	}))
	defer srv.Close()

	file := t.TempDir() + "/a.json"
	if err := os.WriteFile(file, []byte(`[{"tuple_key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"expectation":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		cmd  func(*Command) *cobra.Command
		args []string
	}{
		{"ofga assertions read", (*Command).readCmd, []string{modelID}},
		{"ofga assertions test", (*Command).testCmd, []string{modelID}},
		{"ofga assertions write", (*Command).writeCmd, []string{"--file", file, "--force"}},
		{"ofga assertions write", (*Command).writeCmd, []string{"--file", file, "--dry-run"}},
	} {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			cfg := config.New()
			cfg.Set("default", config.Profile{APIURL: srv.URL, StoreID: modelID, ModelID: modelID})
			a := cli.New(log.New(io.Discard), cfg, "test")
			a.JSON = true
			cmd := tc.cmd(New(a))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(tc.args)
			// assertions test exits non-zero when an expectation fails; the
			// document is still printed and still has to validate.
			_ = cmd.Execute()
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}
}
