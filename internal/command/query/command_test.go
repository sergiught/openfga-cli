package query

import (
	"bytes"
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
