package base

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

// complete drives cobra's completion protocol the way a shell does, returning
// the suggested values.
func complete(t *testing.T, args ...string) []string {
	t.Helper()
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"__complete"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	var values []string
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		// The trailing ":N" line is the directive, not a suggestion.
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		// A suggestion may carry a tab-separated description.
		values = append(values, strings.SplitN(line, "\t", 2)[0])
	}
	return values
}

// Flags with a closed set of values used to fall back to filename completion,
// which offers the user paths where only a handful of words are valid.
func TestEnumeratedFlagsCompleteTheirValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"consistency", []string{"query", "check", "--consistency", ""}, []string{"HIGHER_CONSISTENCY", "MINIMIZE_LATENCY"}},
		{"file-format", []string{"tuples", "write", "--file-format", ""}, []string{"json", "jsonl", "yaml", "csv"}},
		{"on-duplicate", []string{"tuples", "write", "--on-duplicate", ""}, []string{"error", "ignore"}},
		{"on-missing", []string{"tuples", "delete", "--on-missing", ""}, []string{"error", "ignore"}},
		{"theme", []string{"--theme", ""}, []string{"mono"}},
		{"auth-method", []string{"profiles", "add", "p", "--auth-method", ""}, []string{"none", "api_token", "client_credentials", "private_key_jwt"}},
		{"profiles set key", []string{"profiles", "set", ""}, []string{"api_url", "store_id", "auth_method", "headers"}},
		{"profiles unset key", []string{"profiles", "unset", ""}, []string{"api_url", "store_id", "auth_method", "headers"}},
		{"profiles set auth_method value", []string{"profiles", "set", "auth_method", ""}, []string{"none", "client_credentials"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := complete(t, tc.args...)
			for _, want := range tc.want {
				if !slices.Contains(got, want) {
					t.Errorf("completions for %v = %v, missing %q", tc.args, got, want)
				}
			}
		})
	}
}
