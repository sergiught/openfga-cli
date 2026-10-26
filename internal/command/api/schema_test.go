package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput checks that whatever JSON the server returns
// (an object, an array, or a non-2xx error body echoed on stdout) validates
// against the passthrough schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"object":     {200, `{"stores":[{"id":"1"}]}`},
		"array":      {200, `[1,2,3]`},
		"scalar":     {200, `true`},
		"error body": {404, `{"code":"store_id_not_found","message":"nope"}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			cfg := config.New()
			cfg.Set("default", config.Profile{APIURL: srv.URL})
			a := cli.New(log.New(io.Discard), cfg, "test")
			a.JSON = true
			cmd := New(a).Command()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs([]string{"GET", "/stores"})
			_ = cmd.Execute()
			outputschematest.Validate(t, "ofga api", out.Bytes())
		})
	}
}
