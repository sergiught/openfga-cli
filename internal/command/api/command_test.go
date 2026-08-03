package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/output"
)

func TestRequestBody(t *testing.T) {
	// Inline body as the third positional argument.
	body, err := requestBody([]string{"POST", "/stores", `{"name":"x"}`}, strings.NewReader("unused"))
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := body.(json.RawMessage)
	if !ok || string(raw) != `{"name":"x"}` {
		t.Errorf("requestBody = %v, want the raw JSON", body)
	}

	// Invalid JSON is rejected.
	if _, err := requestBody([]string{"POST", "/stores", `{not json`}, nil); err == nil {
		t.Error("invalid JSON body should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("invalid JSON exit code = %d, want usage", clierr.Code(err))
	}

	// A blank inline body yields no body.
	body, err = requestBody([]string{"GET", "/stores", "   "}, strings.NewReader("unused"))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Errorf("blank body should be nil, got %v", body)
	}
}

func TestRequestBodyStdin(t *testing.T) {
	// A "-" body argument reads from stdin.
	body, err := requestBody([]string{"POST", "/stores", "-"}, strings.NewReader(`{"name":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := body.(json.RawMessage)
	if !ok || string(raw) != `{"name":"y"}` {
		t.Errorf("requestBody = %v, want the piped JSON", body)
	}

	// Without a body argument, stdin is never read, so an open pipe cannot
	// block GET/DELETE/HEAD. A reader that would panic if read proves it.
	body, err = requestBody([]string{"GET", "/stores"}, panicReader{})
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Errorf("no body argument should yield nil, got %v", body)
	}
}

func TestValidatePathRequiresSameOriginRelativePath(t *testing.T) {
	for _, path := range []string{"https://evil.example/stores", "//evil.example/stores", "stores"} {
		if err := validatePath(path); err == nil {
			t.Errorf("validatePath(%q) should reject cross-origin/non-rooted input", path)
		} else if clierr.Code(err) != clierr.CodeUsage {
			t.Errorf("validatePath(%q) exit code = %d, want usage", path, clierr.Code(err))
		}
	}
	if err := validatePath("/stores?continuation_token=x"); err != nil {
		t.Fatalf("validatePath(relative) = %v", err)
	}
}

func TestValidateMethodClassifiesInvalidInputAsUsage(t *testing.T) {
	if err := validateMethod("GET\nINJECT"); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("invalid method exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
	if err := validateMethod("PROPFIND"); err != nil {
		t.Fatalf("valid extension method rejected: %v", err)
	}
}

func TestRequestBodyReadFailureRemainsRuntimeError(t *testing.T) {
	want := errors.New("read failed")
	_, err := requestBody([]string{"POST", "/stores", "-"}, errorReader{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("requestBody error = %v, want wrapped read error", err)
	}
	if got := clierr.Code(err); got != clierr.CodeError {
		t.Fatalf("read failure exit code = %d, want runtime error", got)
	}
}

func TestPlainResponsePreservesRawJSON(t *testing.T) {
	c := &Command{cli: &cli.CLI{Plain: true}}
	var out bytes.Buffer
	const raw = `{"compact":true,"nested":{"value":1}}`
	if err := c.write(&out, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), raw+"\n"; got != want {
		t.Fatalf("api --plain output = %q, want raw response %q", got, want)
	}
}

// --jq is a global flag this command advertises in its help, so it has to
// actually filter the response rather than being silently dropped.
func TestJQFiltersTheResponse(t *testing.T) {
	defer func(f string) { output.JQ = f }(output.JQ)
	output.JQ = ".stores[0].id"

	c := &Command{cli: &cli.CLI{JSON: true}}
	var out bytes.Buffer
	if err := c.write(&out, []byte(`{"stores":[{"id":"01ABC","name":"prod"}]}`)); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "01ABC" {
		t.Fatalf("api --jq output = %q, want the filtered id", got)
	}
}

// A filter over a non-JSON body has nothing to work with; failing beats
// printing the raw bytes as though the filter had run.
func TestJQOnNonJSONResponseFails(t *testing.T) {
	defer func(f string) { output.JQ = f }(output.JQ)
	output.JQ = ".id"

	c := &Command{cli: &cli.CLI{JSON: true}}
	var out bytes.Buffer
	if err := c.write(&out, []byte("not json at all")); err == nil {
		t.Fatal("--jq over a non-JSON body should fail")
	}
}

// panicReader panics if Read is ever called, proving stdin is not consumed.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("stdin should not be read") }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

var _ io.Reader = errorReader{}
