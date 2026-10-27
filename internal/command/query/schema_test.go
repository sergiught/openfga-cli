package query

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each query command with --json and checks
// the document against the published schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/batch-check"):
			_, _ = w.Write([]byte(`{"result":{"c0":{"allowed":true},"c1":{"error":{"code":"validation_error","message":"bad"}}}}`))
		case strings.HasSuffix(r.URL.Path, "/check"):
			_, _ = w.Write([]byte(`{"allowed":true,"resolution":""}`))
		case strings.HasSuffix(r.URL.Path, "/expand"):
			_, _ = w.Write([]byte(`{"tree":{"root":{"name":"doc:1#viewer","leaf":{"users":{"users":["user:anne"]}}}}}`))
		case strings.HasSuffix(r.URL.Path, "/list-objects"):
			_, _ = w.Write([]byte(`{"objects":["doc:1","doc:2"]}`))
		case strings.HasSuffix(r.URL.Path, "/list-users"):
			_, _ = w.Write([]byte(`{"users":[{"object":{"type":"user","id":"anne"}},{"userset":{"type":"team","id":"a","relation":"member"}},{"wildcard":{"type":"user"}}]}`))
		default:
			t.Errorf("unexpected request %s %s", r.URL.Path, body)
		}
	}))
	defer srv.Close()

	batchFile := writeBatchFile(t, "c.json", `[{"user":"user:a","relation":"viewer","object":"doc:1"},{"user":"user:b","relation":"viewer","object":"doc:2"}]`)
	ctxFile := writeBatchFile(t, "ctx.json", `[{"user":"user:c","relation":"viewer","object":"doc:3"}]`)

	cases := []struct {
		path string
		cmd  func(*Command) *cobra.Command
		args []string
	}{
		{"ofga query check", (*Command).checkCmd, []string{"user:anne", "viewer", "doc:1"}},
		{"ofga query check", (*Command).checkCmd, []string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", ctxFile}},
		{"ofga query batch-check", (*Command).batchCheckCmd, []string{"--check", "user:a,viewer,doc:1", "--check", "user:b,viewer,doc:2"}},
		{"ofga query batch-check", (*Command).batchCheckCmd, []string{"--file", batchFile}},
		{"ofga query expand", (*Command).expandCmd, []string{"viewer", "doc:1"}},
		{"ofga query list-objects", (*Command).listObjectsCmd, []string{"user", "viewer", "doc:1"}},
		{"ofga query list-users", (*Command).listUsersCmd, []string{"doc:1", "viewer", "--type", "user"}},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newQueryCLI(t, srv.URL)
			a.JSON = true
			cmd := tc.cmd(New(a))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(tc.args)
			// batch-check exits non-zero when an item errored; the document is
			// still printed and still has to validate.
			_ = cmd.Execute()
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}

	t.Run("ofga query list-relations", func(t *testing.T) {
		rs := newListRelationsServer(t, map[string]bool{"viewer": true})
		a := newQueryCLI(t, rs.srv.URL)
		a.JSON = true
		cmd := New(a).listRelationsCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs([]string{"user:anne", "document:roadmap", "--relation", "viewer", "--relation", "editor"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		outputschematest.Validate(t, "ofga query list-relations", out.Bytes())
	})
}
