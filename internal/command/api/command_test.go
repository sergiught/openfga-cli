package api

import (
	"encoding/json"
	"testing"
)

func TestRequestBody(t *testing.T) {
	// Inline body as the third positional argument.
	body, err := requestBody([]string{"POST", "/stores", `{"name":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := body.(json.RawMessage)
	if !ok || string(raw) != `{"name":"x"}` {
		t.Errorf("requestBody = %v, want the raw JSON", body)
	}

	// Invalid JSON is rejected.
	if _, err := requestBody([]string{"POST", "/stores", `{not json`}); err == nil {
		t.Error("invalid JSON body should error")
	}

	// A blank inline body with no piped stdin yields no body.
	body, err = requestBody([]string{"GET", "/stores", "   "})
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Errorf("blank body should be nil, got %v", body)
	}
}
