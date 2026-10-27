package tuple

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each tuple command with --json and checks
// the document against the published schema, bulk results and dry runs included.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	const (
		readBody    = `{"tuples":[{"key":{"user":"user:anne","relation":"viewer","object":"document:1"},"timestamp":"2024-01-02T03:04:05Z"},{"key":{"user":"user:bob","relation":"viewer","object":"document:2","condition":{"name":"c","context":{"a":1}}},"timestamp":"2024-01-02T03:04:05Z"}],"continuation_token":""}`
		changesBody = `{"changes":[{"tuple_key":{"user":"user:anne","relation":"viewer","object":"document:1"},"operation":"TUPLE_OPERATION_WRITE","timestamp":"2024-01-02T03:04:05Z"}],"continuation_token":""}`
	)
	failing := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/read"):
			_, _ = w.Write([]byte(readBody))
		case strings.HasSuffix(r.URL.Path, "/changes"):
			_, _ = w.Write([]byte(changesBody))
		case failing:
			http.Error(w, `{"code":"validation_error","message":"stop"}`, http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	good := bulkFileOf(t, 3, formatJSON)
	cases := []struct {
		path    string
		sub     string
		args    []string
		failing bool
		wantErr bool
	}{
		{"ofga tuples write", "write", []string{"user:anne", "viewer", "document:1"}, false, false},
		{"ofga tuples write", "write", []string{"user:anne", "viewer", "document:1", "--dry-run"}, false, false},
		{"ofga tuples write", "write", []string{"--file", good, "--dry-run"}, false, false},
		{"ofga tuples write", "write", []string{"--file", good}, false, false},
		{"ofga tuples write", "write", []string{"--file", good}, true, true},
		{"ofga tuples delete", "delete", []string{"user:anne", "viewer", "document:1", "--force"}, false, false},
		{"ofga tuples delete", "delete", []string{"user:anne", "viewer", "document:1", "--dry-run"}, false, false},
		{"ofga tuples delete", "delete", []string{"--file", good, "--dry-run"}, false, false},
		{"ofga tuples delete", "delete", []string{"--file", good, "--force"}, false, false},
		{"ofga tuples delete", "delete", []string{"--file", good, "--force"}, true, true},
		{"ofga tuples read", "read", nil, false, false},
		{"ofga tuples changes", "changes", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			failing = tc.failing
			a := newHumanTupleCLI(t, srv.URL)
			a.JSON = true
			c := New(a)
			cmd := map[string]func() *cobra.Command{
				"write": c.writeCmd, "delete": c.deleteCmd, "read": c.readCmd, "changes": c.changesCmd,
			}[tc.sub]()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}
}
