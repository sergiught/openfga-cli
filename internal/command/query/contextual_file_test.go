package query

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

type bodyRecorder struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newBodyRecorder(t *testing.T) *bodyRecorder {
	t.Helper()
	r := &bodyRecorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(b))
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{"allowed":true,"objects":[],"users":[],"result":{"c0":{"allowed":true}}}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *bodyRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// contextualUsers digs contextual_tuples.tuple_keys[].user out of a request
// body, wherever the command puts it.
func contextualUsers(t *testing.T, body string) []string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if checks, ok := doc["checks"].([]any); ok && len(checks) > 0 {
		doc = checks[0].(map[string]any)
	}
	ct, ok := doc["contextual_tuples"].(map[string]any)
	if !ok {
		t.Fatalf("no contextual_tuples in %s", body)
	}
	var users []string
	for _, k := range ct["tuple_keys"].([]any) {
		users = append(users, k.(map[string]any)["user"].(string))
	}
	return users
}

func writeTupleFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoTuplesJSON = `[{"user":"user:a","relation":"viewer","object":"doc:1"},{"user":"user:b","relation":"viewer","object":"doc:2"}]`

func TestContextualTuplesFileOnEveryQueryCommand(t *testing.T) {
	configtest.Isolate(t)
	tests := []struct {
		name string
		cmd  func(*cli.CLI) *cobra.Command
		args []string
	}{
		{"check", func(a *cli.CLI) *cobra.Command { return New(a).checkCmd() }, []string{"user:anne", "viewer", "doc:1"}},
		{"batch-check", func(a *cli.CLI) *cobra.Command { return New(a).batchCheckCmd() }, []string{"--check", "user:anne,viewer,doc:1"}},
		{"list-objects", func(a *cli.CLI) *cobra.Command { return New(a).listObjectsCmd() }, []string{"document", "viewer", "user:anne"}},
		{"list-users", func(a *cli.CLI) *cobra.Command { return New(a).listUsersCmd() }, []string{"document:roadmap", "viewer", "--type", "user"}},
		{"list-relations", func(a *cli.CLI) *cobra.Command { return New(a).listRelationsCmd() }, []string{"user:anne", "document:roadmap", "--relation", "viewer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newBodyRecorder(t)
			file := writeTupleFile(t, "ct.json", twoTuplesJSON)
			cmd := tt.cmd(newQueryCLI(t, rec.srv.URL))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append(append([]string{}, tt.args...),
				"--contextual-tuples-file", file, "--contextual-tuple", "user:c,viewer,doc:3"))
			_ = cmd.Execute()
			if rec.count() == 0 {
				t.Fatal("no request reached the server")
			}
			got := contextualUsers(t, rec.bodies[0])
			if want := "user:a user:b user:c"; strings.Join(got, " ") != want {
				t.Fatalf("contextual users = %v, want %s", got, want)
			}
		})
	}
}

func TestContextualTuplesFileFromStdin(t *testing.T) {
	configtest.Isolate(t)
	tests := []struct{ name, in string }{
		{"jsonl", `{"user":"user:a","relation":"viewer","object":"doc:1"}` + "\n" +
			`{"user":"user:b","relation":"viewer","object":"doc:2"}` + "\n"},
		{"json tuples object", `{"tuples":[{"user":"user:a","relation":"viewer","object":"doc:1"},` +
			`{"user":"user:b","relation":"viewer","object":"doc:2"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newBodyRecorder(t)
			cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
			cmd.SetIn(strings.NewReader(tt.in))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", "-"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := contextualUsers(t, rec.bodies[0]); len(got) != 2 {
				t.Fatalf("contextual users = %v, want 2", got)
			}
		})
	}
}

func TestContextualTuplesFileMalformedJSONLLineIsValidation(t *testing.T) {
	configtest.Isolate(t)
	rec := newBodyRecorder(t)
	cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
	cmd.SetIn(strings.NewReader(`{"user":"user:a","relation":"viewer","object":"doc:1"}` + "\n" + `{"user":` + "\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", "-"})
	err := cmd.Execute()
	if got := clierr.Classify(err); got != clierr.KindValidation {
		t.Fatalf("kind = %q, want validation; err=%v", got, err)
	}
	if n := rec.count(); n != 0 {
		t.Fatalf("server received %d requests, want 0", n)
	}
}

func TestContextualTuplesFileInvalidIsValidation(t *testing.T) {
	configtest.Isolate(t)
	rec := newBodyRecorder(t)
	file := writeTupleFile(t, "bad.json", `[{"user":"user:a"}]`)
	cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", file})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := clierr.Classify(err); got != clierr.KindValidation {
		t.Fatalf("kind = %q, want validation; err=%v", got, err)
	}
	if n := rec.count(); n != 0 {
		t.Fatalf("server received %d requests, want 0", n)
	}
}

func TestContextualTuplesFileFromStdinSingleLineJSONL(t *testing.T) {
	configtest.Isolate(t)
	rec := newBodyRecorder(t)
	cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
	cmd.SetIn(strings.NewReader(`{"user":"user:a","relation":"viewer","object":"doc:1"}` + "\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := contextualUsers(t, rec.bodies[0]); len(got) != 1 || got[0] != "user:a" {
		t.Fatalf("contextual users = %v, want [user:a]", got)
	}
}

func TestContextualTuplesFileErrorIsZeroBasedWithIndex(t *testing.T) {
	configtest.Isolate(t)
	rec := newBodyRecorder(t)
	file := writeTupleFile(t, "bad.json", `[{"user":"user:a","relation":"viewer","object":"doc:1"},{"user":"nope","relation":"viewer","object":"doc:1"}]`)
	cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--contextual-tuples-file", file})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "tuple 1:") {
		t.Fatalf("err = %v, want a 0-based \"tuple 1:\" message", err)
	}
	found := false
	for _, d := range clierr.NewEnvelope(err).Error.Details {
		if d["index"] == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("details lack index 1: %v", clierr.NewEnvelope(err).Error.Details)
	}
}

// A file that can't be read is a runtime failure (exit 1), as it is for
// tuples write --file; only invalid content is a usage error (exit 2).
func TestContextualTuplesFileExitCodes(t *testing.T) {
	configtest.Isolate(t)
	missing := filepath.Join(t.TempDir(), "missing.json")
	invalid := writeTupleFile(t, "bad.json", `[{"user":"user:a"}]`)
	tests := []struct {
		name string
		cmd  func(*cli.CLI) *cobra.Command
		args []string
	}{
		{"check", func(a *cli.CLI) *cobra.Command { return New(a).checkCmd() }, []string{"user:anne", "viewer", "doc:1"}},
		{"batch-check", func(a *cli.CLI) *cobra.Command { return New(a).batchCheckCmd() }, []string{"--check", "user:anne,viewer,doc:1"}},
		{"list-objects", func(a *cli.CLI) *cobra.Command { return New(a).listObjectsCmd() }, []string{"document", "viewer", "user:anne"}},
		{"list-users", func(a *cli.CLI) *cobra.Command { return New(a).listUsersCmd() }, []string{"document:roadmap", "viewer", "--type", "user"}},
		{"list-relations", func(a *cli.CLI) *cobra.Command { return New(a).listRelationsCmd() }, []string{"user:anne", "document:roadmap", "--relation", "viewer"}},
	}
	for _, tt := range tests {
		for _, file := range []struct {
			path string
			code int
			kind clierr.Kind
		}{{missing, clierr.CodeError, clierr.KindError}, {invalid, clierr.CodeUsage, clierr.KindValidation}} {
			t.Run(tt.name+"/"+filepath.Base(file.path), func(t *testing.T) {
				rec := newBodyRecorder(t)
				cmd := tt.cmd(newQueryCLI(t, rec.srv.URL))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append(append([]string{}, tt.args...), "--contextual-tuples-file", file.path))
				err := cmd.Execute()
				if clierr.Code(err) != file.code || clierr.Classify(err) != file.kind {
					t.Fatalf("code=%d kind=%q, want %d %q; err=%v", clierr.Code(err), clierr.Classify(err), file.code, file.kind, err)
				}
				if rec.count() != 0 {
					t.Fatalf("server received %d requests, want 0", rec.count())
				}
			})
		}
	}
}

func TestBatchCheckRejectsBothStdinFlags(t *testing.T) {
	srv, bodies := batchRecorder(t)
	_, err := runBatch(t, srv.URL, batchJSON, "--file", "-", "--contextual-tuples-file", "-")
	if err == nil || clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("err = %v (code %d), want a usage error", err, clierr.Code(err))
	}
	if !strings.Contains(err.Error(), "both read stdin") {
		t.Fatalf("err = %v, want the both-stdin message", err)
	}
	if len(bodies()) != 0 {
		t.Fatal("a request was sent")
	}
}
