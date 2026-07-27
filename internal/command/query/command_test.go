package query

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
	if err := writePlainBatchResult(&out, true, "user:anne viewer\nadmin\tdoc:1\x1b[31m"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "allowed\tuser:anne viewer admin doc:1\n"; got != want {
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
