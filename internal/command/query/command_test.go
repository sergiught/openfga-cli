package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
)

func newQueryCLI(t *testing.T, apiURL string) *cli.CLI {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	return cli.New(log.New(io.Discard), cfg, "test")
}

func TestAllowedWord(t *testing.T) {
	if allowedWord(true) != "allowed" {
		t.Error("allowedWord(true) should be allowed")
	}
	if allowedWord(false) != "denied" {
		t.Error("allowedWord(false) should be denied")
	}
}

func TestPlainBatchLabelCannotInjectRecords(t *testing.T) {
	var out bytes.Buffer
	if err := writePlainBatchResult(&out, "allowed", "user:anne viewer\nadmin\tdoc:1\x1b[31m", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "allowed\tuser:anne viewer admin doc:1\t\n"; got != want {
		t.Fatalf("plain batch result = %q, want %q", got, want)
	}
}

func TestPlainBatchDetailCannotInjectRecords(t *testing.T) {
	var out bytes.Buffer
	if err := writePlainBatchResult(&out, "error", "user:anne viewer doc:1", "bad\trelation\nvalue"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "error\tuser:anne viewer doc:1\tbad relation value\n"; got != want {
		t.Fatalf("plain batch result = %q, want %q", got, want)
	}
}

func TestBatchCheckValidatesInputBeforeClientCreation(t *testing.T) {
	cmd := (&Command{}).batchCheckCmd()
	cmd.SetArgs([]string{"--check", "anne,viewer,doc:1"})
	err := cmd.Execute()
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", got, err)
	}
}

// batchCheckMockServer serves POST /stores/{id}/batch-check, enforcing the
// real server's 50-item cap and returning results built by resultFor for the
// checks it receives.
func batchCheckMockServer(t *testing.T, resultFor func(correlationID string) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Checks []struct {
				CorrelationID string `json:"correlation_id"`
			} `json:"checks"`
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Checks) > 50 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"validation_error","message":"the number of checks exceeds the maximum allowed"}`))
			return
		}
		result := make(map[string]any, len(body.Checks))
		for _, c := range body.Checks {
			if r := resultFor(c.CorrelationID); r != nil {
				result[c.CorrelationID] = r
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
}

// TestBatchCheckHandlesMoreThan50Checks (CLI-72): the server caps a single
// /batch-check request at 50 items, so 60 --check flags must be chunked by
// BatchCheckAll rather than sent as one request that would 400. Results must
// come back in input order (order is not guaranteed by the response map).
func TestBatchCheckHandlesMoreThan50Checks(t *testing.T) {
	srv := batchCheckMockServer(t, func(string) map[string]any {
		return map[string]any{"allowed": true}
	})
	defer srv.Close()

	const total = 60
	args := make([]string, 0, total*2)
	for i := range total {
		args = append(args, "--check", fmt.Sprintf("user:u%d,viewer,doc:%d", i, i))
	}

	cmd := New(newQueryCLI(t, srv.URL)).batchCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("batch-check with %d checks: %v", total, err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != total {
		t.Fatalf("got %d result line(s), want %d", len(lines), total)
	}
	for i, line := range lines {
		want := fmt.Sprintf("user:u%d viewer doc:%d", i, i)
		if !strings.Contains(line, want) {
			t.Errorf("line %d = %q, want to contain %q (results must preserve input order)", i, line, want)
		}
	}
}

// TestBatchCheckPerItemErrorIsNotDenied (CLI-73): a per-item Error in the
// response must render distinctly from a real "denied" result, in both human
// and --plain output, and must make the command exit non-zero.
func TestBatchCheckPerItemErrorIsNotDenied(t *testing.T) {
	srv := batchCheckMockServer(t, func(id string) map[string]any {
		if id == "c1" {
			return map[string]any{"allowed": false, "error": map[string]any{"message": "relation not found"}}
		}
		return map[string]any{"allowed": true}
	})
	defer srv.Close()

	t.Run("human", func(t *testing.T) {
		cmd := New(newQueryCLI(t, srv.URL)).batchCheckCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--check", "user:anne,viewer,doc:1", "--check", "user:bob,viewr,doc:1"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected a non-nil error when an item's result carries an error")
		}
		if code := clierr.Code(err); code == 0 {
			t.Errorf("exit code = %d, want non-zero", code)
		}
		got := out.String()
		if strings.Contains(strings.ToLower(got), "denied") {
			t.Errorf("output = %q, must not render the errored item as denied", got)
		}
		if !strings.Contains(got, "ERROR") {
			t.Errorf("output = %q, want an ERROR marker for the errored item", got)
		}
	})

	t.Run("plain", func(t *testing.T) {
		output.Plain = true
		t.Cleanup(func() { output.Plain = false })

		cmd := New(newQueryCLI(t, srv.URL)).batchCheckCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--check", "user:anne,viewer,doc:1", "--check", "user:bob,viewr,doc:1"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected a non-nil error when an item's result carries an error")
		}
		if code := clierr.Code(err); code == 0 {
			t.Errorf("exit code = %d, want non-zero", code)
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("got %d line(s), want 2", len(lines))
		}
		if strings.HasPrefix(lines[1], "denied") {
			t.Errorf("line 2 = %q, must not render the errored item as denied", lines[1])
		}
		if !strings.HasPrefix(lines[1], "error\t") {
			t.Errorf("line 2 = %q, want it to start with the error marker", lines[1])
		}
	})
}

// TestBatchCheckMissingCorrelationIDIsNotDenied (CLI-73): a correlation ID the
// server never returned a result for (e.g. a failed chunk) must render as an
// error, not silently render as "denied".
func TestBatchCheckMissingCorrelationIDIsNotDenied(t *testing.T) {
	srv := batchCheckMockServer(t, func(id string) map[string]any {
		if id == "c0" {
			// c1's correlation ID is simply absent from the result map.
			return map[string]any{"allowed": true}
		}
		return nil
	})
	defer srv.Close()

	cmd := New(newQueryCLI(t, srv.URL)).batchCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check", "user:anne,viewer,doc:1", "--check", "user:bob,editor,doc:1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error when a correlation ID is missing from the response")
	}
	if code := clierr.Code(err); code == 0 {
		t.Errorf("exit code = %d, want non-zero", code)
	}
	got := out.String()
	if strings.Contains(strings.ToLower(got), "denied") {
		t.Errorf("output = %q, must not render the missing result as denied", got)
	}
}

// TestBatchCheckAllSuccessExitsZero (CLI-72/CLI-73 regression guard): the
// happy path must keep exiting 0 and emitting the raw SDK response unchanged
// under --json.
func TestBatchCheckAllSuccessExitsZero(t *testing.T) {
	srv := batchCheckMockServer(t, func(string) map[string]any {
		return map[string]any{"allowed": true}
	})
	defer srv.Close()

	a := newQueryCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).batchCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check", "user:anne,viewer,doc:1", "--check", "user:bob,editor,doc:1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("all-success batch-check should exit 0, got: %v", err)
	}
	var res openfga.BatchCheckResponse
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("--json output not valid BatchCheckResponse JSON: %v (%q)", err, out.String())
	}
	if len(res.Result) != 2 {
		t.Fatalf("got %d result(s), want 2", len(res.Result))
	}
	for id, r := range res.Result {
		if !r.Allowed {
			t.Errorf("result %s: allowed = false, want true", id)
		}
	}
}

func TestContextualFlagsRegistered(t *testing.T) {
	tests := []struct {
		name  string
		cmd   *cobra.Command
		flags []string
	}{
		{"list-objects", (&Command{}).listObjectsCmd(), []string{"context", "contextual-tuple"}},
		{"list-users", (&Command{}).listUsersCmd(), []string{"context", "contextual-tuple"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range tt.flags {
				if tt.cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s missing --%s flag", tt.name, name)
				}
			}
		})
	}
}

func TestMalformedContextIsUsageError(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"check bad context", []string{"user:anne", "viewer", "doc:1", "--context", "not json"}},
		{"check bad contextual-tuple", []string{"user:anne", "viewer", "doc:1", "--contextual-tuple", "anne,viewer,doc:1"}},
		{"list-objects bad context", []string{"document", "viewer", "user:anne", "--context", "not json"}},
		{"list-objects bad contextual-tuple", []string{"document", "viewer", "user:anne", "--contextual-tuple", "anne,viewer,doc:1"}},
		{"list-users bad context", []string{"document:roadmap", "viewer", "--type", "user", "--context", "not json"}},
		{"list-users bad contextual-tuple", []string{"document:roadmap", "viewer", "--type", "user", "--contextual-tuple", "anne,viewer,doc:1"}},
	}
	cmds := map[string]func() *cobra.Command{
		"check":        func() *cobra.Command { return (&Command{}).checkCmd() },
		"list-objects": func() *cobra.Command { return (&Command{}).listObjectsCmd() },
		"list-users":   func() *cobra.Command { return (&Command{}).listUsersCmd() },
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := strings.SplitN(tt.name, " ", 2)[0]
			cmd := cmds[key]()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if got := clierr.Code(err); got != clierr.CodeUsage {
				t.Fatalf("exit code = %d, want usage; err=%v", got, err)
			}
		})
	}
}

func TestCheckPlainEmitsAllowedRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer srv.Close()

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newQueryCLI(t, srv.URL)).checkCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "allowed\ttrue\n"; got != want {
		t.Fatalf("check --plain = %q, want %q", got, want)
	}
}

func TestExpandTableRendersTreeNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tree":{"root":"document:roadmap#viewer"}}`))
	}))
	defer srv.Close()

	a := newQueryCLI(t, srv.URL)
	a.Output = "table"
	cmd := New(a).expandCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"viewer", "document:roadmap"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "{") {
		t.Fatalf("expand -o table emitted JSON, want tree outline: %q", got)
	}
	if got != "root: document:roadmap#viewer\n" {
		t.Fatalf("expand -o table = %q", got)
	}
}

func TestFormatUser(t *testing.T) {
	tests := []struct {
		name string
		user openfga.User
		want string
	}{
		{name: "object", user: openfga.User{Object: &openfga.FGAObject{Type: "user", ID: "anne"}}, want: "user:anne"},
		{name: "userset", user: openfga.User{Userset: &openfga.UsersetUser{Type: "team", ID: "eng", Relation: "member"}}, want: "team:eng#member"},
		{name: "wildcard", user: openfga.User{Wildcard: &openfga.TypedWildcard{Type: "user"}}, want: "user:*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatUser(tt.user); got != tt.want {
				t.Errorf("formatUser = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseContextualTuples(t *testing.T) {
	got, err := parseContextualTuples([]string{"user:anne,viewer,doc:1", "user:bob,editor,doc:2"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.TupleKeys) != 2 {
		t.Fatalf("expected 2 contextual tuples, got %+v", got)
	}
	if got.TupleKeys[0].User != "user:anne" || got.TupleKeys[0].Object != "doc:1" {
		t.Errorf("first tuple parsed wrong: %+v", got.TupleKeys[0])
	}

	if _, err := parseContextualTuples([]string{"user:anne,viewer"}); err == nil {
		t.Error("wrong field count should error")
	}
	// A malformed triple (bad user) must be rejected via fga.ParseTuple (ENG-2).
	if _, err := parseContextualTuples([]string{"anne,viewer,doc:1"}); err == nil {
		t.Error("malformed user should be rejected")
	}

	got, err = parseContextualTuples(nil)
	if err != nil || got != nil {
		t.Errorf("empty input should yield (nil, nil), got (%v, %v)", got, err)
	}
}

func TestParseContext(t *testing.T) {
	m, err := parseContext(`{"a":1}`)
	if err != nil || m["a"] != float64(1) {
		t.Errorf("parseContext = %v, %v", m, err)
	}
	if m, err := parseContext(""); err != nil || m != nil {
		t.Errorf("empty context should be (nil,nil), got (%v,%v)", m, err)
	}
	if _, err := parseContext("not json"); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestResolveArgsCombinesPositionalsAndFlags(t *testing.T) {
	got, err := resolveArgs(
		[]string{"viewer"},
		[]string{"document", "", "user:anne"},
		[]string{"type", "relation", "user"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "document|viewer|user:anne" {
		t.Fatalf("resolveArgs() = %v", got)
	}
	if _, err := resolveArgs(nil, []string{"document", "", ""}, []string{"type", "relation", "user"}); err == nil {
		t.Fatal("resolveArgs should report missing named fields")
	}
}

// listRelationsServer answers the two endpoints list-relations may touch: the
// model read that derives the candidate relations, and the batch-check that
// tests them. batchBody captures what was actually asked.
type listRelationsServer struct {
	srv        *httptest.Server
	modelReads int
	batchBody  string
}

func newListRelationsServer(t *testing.T, allowed map[string]bool) *listRelationsServer {
	t.Helper()
	s := &listRelationsServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "authorization-models") {
			s.modelReads++
			_, _ = w.Write([]byte(`{"authorization_models":[{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","schema_version":"1.1",
				"type_definitions":[
					{"type":"user"},
					{"type":"document","relations":{"viewer":{"this":{}},"editor":{"this":{}},"owner":{"this":{}}},
					 "metadata":{"relations":{
						"viewer":{"directly_related_user_types":[{"type":"user"}]},
						"editor":{"directly_related_user_types":[{"type":"user"}]},
						"owner":{"directly_related_user_types":[{"type":"user"}]}}}}]}]}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.batchBody = string(body)
		var req struct {
			Checks []struct {
				CorrelationID string `json:"correlation_id"`
				TupleKey      struct {
					Relation string `json:"relation"`
				} `json:"tuple_key"`
			} `json:"checks"`
		}
		_ = json.Unmarshal(body, &req)
		result := map[string]map[string]bool{}
		for _, c := range req.Checks {
			result[c.CorrelationID] = map[string]bool{"allowed": allowed[c.TupleKey.Relation]}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestListRelationsExplicitRelationsSkipTheModel(t *testing.T) {
	srv := newListRelationsServer(t, map[string]bool{"viewer": true, "editor": false})

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newQueryCLI(t, srv.srv.URL)).listRelationsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "document:roadmap", "--relation", "viewer", "--relation", "editor"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "viewer\n"; got != want {
		t.Fatalf("list-relations --plain = %q, want %q", got, want)
	}
	if srv.modelReads != 0 {
		t.Errorf("read the model %d time(s); explicit --relation should not need it", srv.modelReads)
	}
}

func TestListRelationsWithoutFlagDerivesCandidatesFromTheModel(t *testing.T) {
	srv := newListRelationsServer(t, map[string]bool{"viewer": true, "owner": true})

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newQueryCLI(t, srv.srv.URL)).listRelationsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "document:roadmap"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if srv.modelReads != 1 {
		t.Fatalf("read the model %d time(s), want 1", srv.modelReads)
	}
	// The model declares editor, owner and viewer; all three must be tested,
	// and the allowed ones reported in the model's sorted order.
	for _, rel := range []string{"editor", "owner", "viewer"} {
		if !strings.Contains(srv.batchBody, `"relation":"`+rel+`"`) {
			t.Errorf("batch-check body did not test %q: %s", rel, srv.batchBody)
		}
	}
	if got, want := out.String(), "owner\nviewer\n"; got != want {
		t.Fatalf("list-relations --plain = %q, want %q", got, want)
	}
}

func TestListRelationsEmitsJSONArray(t *testing.T) {
	srv := newListRelationsServer(t, map[string]bool{"viewer": true})

	a := newQueryCLI(t, srv.srv.URL)
	a.JSON = true
	cmd := New(a).listRelationsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "document:roadmap", "--relation", "viewer", "--relation", "editor"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not a JSON array: %v (%q)", err, out.String())
	}
	if len(got) != 1 || got[0] != "viewer" {
		t.Fatalf("--json = %v, want [viewer]", got)
	}
}

func TestListRelationsNoneAllowedPrintsNothingOnStdout(t *testing.T) {
	srv := newListRelationsServer(t, nil)

	cmd := New(newQueryCLI(t, srv.srv.URL)).listRelationsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"user:anne", "document:roadmap", "--relation", "viewer"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "no relations") {
		t.Fatalf("stderr = %q, want a no-relations notice", errOut.String())
	}
}

func TestListRelationsUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing object", []string{"user:anne"}},
		{"malformed user", []string{"anne", "document:roadmap"}},
		{"malformed object", []string{"user:anne", "roadmap"}},
		{"duplicate relation", []string{"user:anne", "document:roadmap", "--relation", "viewer", "--relation", "viewer"}},
		{"bad context", []string{"user:anne", "document:roadmap", "--relation", "viewer", "--context", "not json"}},
		{"bad contextual tuple", []string{"user:anne", "document:roadmap", "--relation", "viewer", "--contextual-tuple", "anne,viewer,doc:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newListRelationsServer(t, nil)
			cmd := New(newQueryCLI(t, srv.srv.URL)).listRelationsCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if got := clierr.Code(err); got != clierr.CodeUsage {
				t.Fatalf("exit code = %d, want usage; err=%v", got, err)
			}
		})
	}
}

// consistencyRecorder captures the request bodies a command sends so a test can
// assert what actually reached the wire, since --consistency has no visible
// effect on the response.
type consistencyRecorder struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newConsistencyRecorder(t *testing.T, response string) *consistencyRecorder {
	t.Helper()
	r := &consistencyRecorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(body))
		r.mu.Unlock()
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *consistencyRecorder) first(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("no request reached the server")
	}
	return r.bodies[0]
}

func TestConsistencyFlagRegisteredOnReadCommands(t *testing.T) {
	cmds := map[string]*cobra.Command{
		"check":          (&Command{}).checkCmd(),
		"batch-check":    (&Command{}).batchCheckCmd(),
		"expand":         (&Command{}).expandCmd(),
		"list-objects":   (&Command{}).listObjectsCmd(),
		"list-users":     (&Command{}).listUsersCmd(),
		"list-relations": (&Command{}).listRelationsCmd(),
	}
	for name, cmd := range cmds {
		if cmd.Flags().Lookup("consistency") == nil {
			t.Errorf("%s missing --consistency flag", name)
		}
	}
}

func TestConsistencyFlagReachesTheWire(t *testing.T) {
	tests := []struct {
		name     string
		response string
		cmd      func(*cli.CLI) *cobra.Command
		args     []string
	}{
		{"check", `{"allowed":true}`, func(a *cli.CLI) *cobra.Command { return New(a).checkCmd() },
			[]string{"user:anne", "viewer", "doc:1"}},
		// The result must carry the command's own correlation ID (c0 for the
		// first --check): batch-check resolves each item by ID and treats a
		// missing entry as a failure rather than a denial, so an empty result
		// map would fail the command before the body assertion is reached.
		{"batch-check", `{"result":{"c0":{"allowed":true}}}`, func(a *cli.CLI) *cobra.Command { return New(a).batchCheckCmd() },
			[]string{"--check", "user:anne,viewer,doc:1"}},
		{"expand", `{"tree":{"root":"doc:1#viewer"}}`, func(a *cli.CLI) *cobra.Command { return New(a).expandCmd() },
			[]string{"viewer", "doc:1"}},
		{"list-objects", `{"objects":[]}`, func(a *cli.CLI) *cobra.Command { return New(a).listObjectsCmd() },
			[]string{"document", "viewer", "user:anne"}},
		{"list-users", `{"users":[]}`, func(a *cli.CLI) *cobra.Command { return New(a).listUsersCmd() },
			[]string{"document:roadmap", "viewer", "--type", "user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("explicit value is sent", func(t *testing.T) {
				rec := newConsistencyRecorder(t, tt.response)
				cmd := tt.cmd(newQueryCLI(t, rec.srv.URL))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append(append([]string{}, tt.args...), "--consistency", "minimize_latency"))
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if got := rec.first(t); !strings.Contains(got, `"consistency":"MINIMIZE_LATENCY"`) {
					t.Fatalf("request body = %s, want MINIMIZE_LATENCY", got)
				}
			})
			t.Run("unset keeps the higher-consistency default", func(t *testing.T) {
				rec := newConsistencyRecorder(t, tt.response)
				cmd := tt.cmd(newQueryCLI(t, rec.srv.URL))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(tt.args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if got := rec.first(t); !strings.Contains(got, `"consistency":"HIGHER_CONSISTENCY"`) {
					t.Fatalf("request body = %s, want HIGHER_CONSISTENCY", got)
				}
			})
			t.Run("unknown value is a usage error", func(t *testing.T) {
				rec := newConsistencyRecorder(t, tt.response)
				cmd := tt.cmd(newQueryCLI(t, rec.srv.URL))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append(append([]string{}, tt.args...), "--consistency", "eventual"))
				err := cmd.Execute()
				if got := clierr.Code(err); got != clierr.CodeUsage {
					t.Fatalf("exit code = %d, want usage; err=%v", got, err)
				}
			})
		})
	}
}

func TestListRelationsUnknownTypeIsUsageError(t *testing.T) {
	srv := newListRelationsServer(t, nil)
	cmd := New(newQueryCLI(t, srv.srv.URL)).listRelationsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// widget is not in the model, so there is no candidate relation set: that
	// is a mistake in the command line, not a server failure.
	cmd.SetArgs([]string{"user:anne", "widget:1"})
	err := cmd.Execute()
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", got, err)
	}
}

func TestExplicitUnspecifiedConsistencyIsSent(t *testing.T) {
	rec := newConsistencyRecorder(t, `{"allowed":true}`)
	cmd := New(newQueryCLI(t, rec.srv.URL)).checkCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"user:anne", "viewer", "doc:1", "--consistency", "UNSPECIFIED"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := rec.first(t); !strings.Contains(got, `"consistency":"UNSPECIFIED"`) {
		t.Fatalf("request body = %s, want the explicit UNSPECIFIED to survive the client default", got)
	}
}
