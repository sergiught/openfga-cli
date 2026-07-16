package query

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/clierr"
)

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
