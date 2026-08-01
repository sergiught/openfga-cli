package output

import (
	"bytes"
	"strings"
	"testing"
)

type jqStore struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestJQFilters(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)

	items := []jqStore{{ID: "01AAA", Name: "dev", N: 1}, {ID: "01BBB", Name: "prod", N: 2}}

	tests := []struct {
		name   string
		filter string
		want   string
	}{
		// The literal case from openfga/cli#395: extract an id for a shell var.
		{"field of first", ".[0].id", "01AAA\n"},
		{"field of each", ".[].id", "01AAA\n01BBB\n"},
		{"length", "length", "2\n"},
		{"select", `.[] | select(.name=="prod") | .id`, "01BBB\n"},
		// Non-string results stay JSON so they can be re-parsed.
		{"object", ".[0] | {id}", `{"id":"01AAA"}` + "\n"},
		{"array", "[.[].n]", "[1,2]\n"},
		{"number", ".[1].n", "2\n"},
		{"null", ".[0].missing", "null\n"},
		{"no results", ".[] | select(.name==\"nope\")", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			JQ = tt.filter
			var b bytes.Buffer
			if err := Emit(&b, false, items); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if b.String() != tt.want {
				t.Errorf("--jq %q = %q, want %q", tt.filter, b.String(), tt.want)
			}
		})
	}
}

// Strings print bare, as `jq -r` does, so `$(ofga ... --jq .id)` captures the
// value rather than a quoted string.
func TestJQStringsAreRaw(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)
	JQ = ".[0].id"

	var b bytes.Buffer
	if err := Emit(&b, false, []jqStore{{ID: "01AAA"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `"`) {
		t.Errorf("string result should be unquoted, got %q", b.String())
	}
}

// --jq replaces the rendering for YAML too, so the flag behaves the same
// whichever structured format was selected.
func TestJQOverridesYAML(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)
	JQ = ".[0].name"

	var b bytes.Buffer
	if err := Emit(&b, true, []jqStore{{Name: "dev"}}); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "dev\n" {
		t.Errorf("Emit(yaml) with --jq = %q, want %q", got, "dev\n")
	}
}

func TestValidateJQRejectsBadFilter(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)

	JQ = ".["
	if err := ValidateJQ(); err == nil {
		t.Fatal("a syntactically invalid filter should be rejected")
	}
	JQ = ".id"
	if err := ValidateJQ(); err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
	JQ = ""
	if err := ValidateJQ(); err != nil {
		t.Fatalf("empty filter should be a no-op, got %v", err)
	}
}

// A runtime type error must fail the command rather than printing a partial
// result and exiting zero.
func TestJQRuntimeErrorIsReported(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)
	JQ = ".[0] | .id + 1" // string + number

	var b bytes.Buffer
	if err := Emit(&b, false, []jqStore{{ID: "01AAA"}}); err == nil {
		t.Fatal("a jq runtime error should surface")
	}
}

// The streaming path cannot stream under a filter that may need the whole
// document, so it buffers — but the result must be identical to Emit's.
func TestStreamerUnderJQMatchesEmit(t *testing.T) {
	defer func(j string) { JQ = j }(JQ)
	items := []jqStore{{ID: "01AAA", N: 1}, {ID: "01BBB", N: 2}}

	for _, filter := range []string{".[].id", "length", "[.[].n]"} {
		JQ = filter

		var want bytes.Buffer
		if err := Emit(&want, false, items); err != nil {
			t.Fatal(err)
		}

		var got bytes.Buffer
		s := NewStreamer(&got, false)
		for _, it := range items {
			if err := s.Write(it); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}

		if got.String() != want.String() {
			t.Errorf("--jq %q: streamed %q, want %q", filter, got.String(), want.String())
		}
	}
}
