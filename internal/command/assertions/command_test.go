package assertions

import (
	"errors"
	"syscall"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/clierr"
)

func TestParseAssertions(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantN   int
		wantErr bool
	}{
		{name: "bare array", data: `[{"tuple_key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"expectation":true}]`, wantN: 1},
		{name: "wrapper object", data: `{"assertions":[{"tuple_key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"expectation":true},{"tuple_key":{"user":"user:bob","relation":"viewer","object":"doc:1"},"expectation":false}]}`, wantN: 2},
		{name: "empty array", data: `[]`, wantN: 0},
		{name: "invalid json", data: `{not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAssertions([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAssertions err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && len(got) != tt.wantN {
				t.Errorf("parseAssertions returned %d assertions, want %d", len(got), tt.wantN)
			}
		})
	}
}

func TestAssertionFailureOutranksBrokenPipe(t *testing.T) {
	failed := clierr.WithCode(clierr.CodeTestFailed, errors.New("assertion failed"))
	if got := preferAssertionFailure(syscall.EPIPE, failed); clierr.Code(got) != clierr.CodeTestFailed {
		t.Fatalf("error code = %d, want %d", clierr.Code(got), clierr.CodeTestFailed)
	}
}

func TestOutputFailureReturnedWhenAssertionsPass(t *testing.T) {
	if got := preferAssertionFailure(syscall.EPIPE, nil); !errors.Is(got, syscall.EPIPE) {
		t.Fatalf("preferAssertionFailure() = %v, want EPIPE", got)
	}
}

func TestAssertionsDryRunShorthand(t *testing.T) {
	cmd := (&Command{}).writeCmd()
	if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
		t.Fatalf("--dry-run shorthand = %q, want n", got)
	}
}

func TestToTupleKey(t *testing.T) {
	k := toTupleKey(openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"})
	if k.User != "user:anne" || k.Relation != "viewer" || k.Object != "doc:1" {
		t.Errorf("toTupleKey = %+v, want the same user/relation/object", k)
	}
}

func TestWriteHasExplicitReplacementGate(t *testing.T) {
	cmd := (&Command{}).writeCmd()
	if cmd.Flags().Lookup("force") == nil {
		t.Fatal("assertions write must expose --force for non-interactive replacement")
	}
}
