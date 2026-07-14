package api

import (
	"encoding/json"
	"strings"
	"testing"
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

// panicReader panics if Read is ever called, proving stdin is not consumed.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("stdin should not be read") }
