package apilog

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubRT records the request body it received and returns a canned response.
type stubRT struct {
	gotReqBody string
	resp       *http.Response
	err        error
}

func (s *stubRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.gotReqBody = string(b)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newResp(status int, body string) *http.Response {
	h := http.Header{}
	h.Set("Fga-Request-Id", "req-123")
	h.Set("Fga-Query-Duration-Ms", "4")
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTransportCapturesAndRewrapsBodies(t *testing.T) {
	rec := NewRecorder(4)
	stub := &stubRT{resp: newResp(200, `{"allowed":true}`)}
	rt := Transport(stub, rec, "https://api.example")

	req, _ := http.NewRequest(http.MethodPost, "https://api.example/stores/1/check", strings.NewReader(`{"x":1}`))
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	// Downstream transport must have received the FULL request body.
	if stub.gotReqBody != `{"x":1}` {
		t.Fatalf("downstream req body = %q", stub.gotReqBody)
	}
	// Caller must still be able to read the FULL response body.
	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"allowed":true}` {
		t.Fatalf("resp body = %q", got)
	}

	e := rec.Snapshot()[0]
	if e.Method != "POST" || e.Status != 200 {
		t.Fatalf("entry meta wrong: %+v", e)
	}
	if string(e.ReqBody) != `{"x":1}` || string(e.RespBody) != `{"allowed":true}` {
		t.Fatalf("captured bodies wrong: %q / %q", e.ReqBody, e.RespBody)
	}
	if e.ReqHeaders.Get("Authorization") != "Bearer ***redacted***" {
		t.Fatalf("Authorization not redacted: %q", e.ReqHeaders.Get("Authorization"))
	}
	if e.RequestID != "req-123" || e.ServerQueryDuration != "4" {
		t.Fatalf("server headers not captured: %+v", e)
	}
}

func TestTransportErrorPath(t *testing.T) {
	rec := NewRecorder(4)
	stub := &stubRT{err: errors.New("connection refused")}
	rt := Transport(stub, rec, "https://api.example")
	req, _ := http.NewRequest(http.MethodGet, "https://api.example/stores", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("expected error to propagate")
	}
	e := rec.Snapshot()[0]
	if e.Err == "" || e.Status != 0 {
		t.Fatalf("want Err set and no status, got %+v", e)
	}
}

func TestTransportTruncatesLargeBody(t *testing.T) {
	rec := NewRecorder(4)
	big := strings.Repeat("a", maxBodyBytes+100)
	stub := &stubRT{resp: newResp(200, big)}
	rt := Transport(stub, rec, "https://api.example")
	req, _ := http.NewRequest(http.MethodGet, "https://api.example/x", nil)
	resp, _ := rt.RoundTrip(req)
	// Caller still gets the full body despite the stored copy being capped.
	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(big) {
		t.Fatalf("caller body truncated: %d", len(got))
	}
	e := rec.Snapshot()[0]
	if !strings.Contains(string(e.RespBody), "truncated") {
		t.Fatal("stored body should carry a truncation marker")
	}
}

func TestTransportSkipsStreamedBody(t *testing.T) {
	rec := NewRecorder(4)
	stub := &stubRT{resp: newResp(200, `{"result":{}}`)}
	rt := Transport(stub, rec, "https://api.example")
	req, _ := http.NewRequest(http.MethodPost, "https://api.example/stores/1/streamed-list-objects", nil)
	rt.RoundTrip(req)
	if !strings.Contains(string(rec.Snapshot()[0].RespBody), "streamed") {
		t.Fatal("streamed endpoint response body must not be buffered")
	}
}

// TestTransportSkipsNonAPIHost is a security regression test: the SDK routes
// out-of-band OAuth token fetches (client_credentials / private_key_jwt)
// through this same base transport, and those requests carry secrets — a
// client_secret in the request body, a live access_token in the response
// body — that must never be captured into the API Logs pane. Only requests
// to the configured API host may be recorded; a token fetch to a different
// host (the IdP) must pass through completely untouched and unrecorded.
func TestTransportSkipsNonAPIHost(t *testing.T) {
	rec := NewRecorder(4)
	stub := &stubRT{resp: newResp(200, `{"access_token":"eyJLEAKED","expires_in":86400}`)}
	rt := Transport(stub, rec, "https://api.example")

	reqBody := "grant_type=client_credentials&client_id=abc&client_secret=SUPERSECRET"
	req, _ := http.NewRequest(http.MethodPost, "https://login.example/oauth/token", strings.NewReader(reqBody))

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	// The token fetch must NOT be recorded.
	if len(rec.Snapshot()) != 0 {
		t.Fatalf("token fetch to non-API host must not be recorded, got %d entries", len(rec.Snapshot()))
	}
	// The downstream transport must still receive the FULL request body.
	if stub.gotReqBody != reqBody {
		t.Fatalf("downstream req body = %q, want %q", stub.gotReqBody, reqBody)
	}
	// The caller must still be able to read the FULL response body.
	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"access_token":"eyJLEAKED","expires_in":86400}` {
		t.Fatalf("resp body = %q", got)
	}

	// Positive case: a request to the configured API host IS recorded.
	req2, _ := http.NewRequest(http.MethodGet, "https://api.example/stores/1/check", nil)
	resp2, err := rt.RoundTrip(req2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if len(rec.Snapshot()) != 1 {
		t.Fatalf("expected exactly one recorded entry for the API-host request, got %d", len(rec.Snapshot()))
	}
}

// TestTransportRedactsSecretFieldsInBody is a security regression test:
// defense-in-depth against a deployment where the OAuth token endpoint shares
// a host with the API (e.g. behind a gateway), which would otherwise bypass
// the host-based exclusion in TestTransportSkipsNonAPIHost. Any captured body
// must have known secret field values masked, in both JSON and form encodings.
func TestTransportRedactsSecretFieldsInBody(t *testing.T) {
	rec := NewRecorder(4)
	respBody := `{"access_token":"eyJLEAKED","refresh_token":"rt-leak","expires_in":86400}`
	stub := &stubRT{resp: newResp(200, respBody)}
	rt := Transport(stub, rec, "https://api.example")

	reqBody := "grant_type=client_credentials&client_id=abc&client_secret=SUPERSECRET"
	req, _ := http.NewRequest(http.MethodPost, "https://api.example/oauth/token", strings.NewReader(reqBody))

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	// The downstream transport must still receive the FULL, unredacted body.
	if stub.gotReqBody != reqBody {
		t.Fatalf("downstream req body = %q, want unredacted %q", stub.gotReqBody, reqBody)
	}
	// The caller must still be able to read the FULL, unredacted response body.
	got, _ := io.ReadAll(resp.Body)
	if string(got) != respBody {
		t.Fatalf("resp body = %q, want unredacted %q", got, respBody)
	}

	e := rec.Snapshot()[0]
	if strings.Contains(string(e.ReqBody), "SUPERSECRET") {
		t.Fatalf("client_secret leaked into captured request body: %q", e.ReqBody)
	}
	if !strings.Contains(string(e.ReqBody), "client_secret=******") {
		t.Fatalf("client_secret not masked in captured request body: %q", e.ReqBody)
	}
	if strings.Contains(string(e.RespBody), "eyJLEAKED") || strings.Contains(string(e.RespBody), "rt-leak") {
		t.Fatalf("tokens leaked into captured response body: %q", e.RespBody)
	}
	if !strings.Contains(string(e.RespBody), `"access_token":"******"`) ||
		!strings.Contains(string(e.RespBody), `"refresh_token":"******"`) {
		t.Fatalf("tokens not masked in captured response body: %q", e.RespBody)
	}
	// Non-sensitive fields must be preserved untouched.
	if !strings.Contains(string(e.RespBody), `"expires_in":86400`) {
		t.Fatalf("non-sensitive field altered: %q", e.RespBody)
	}
}

func TestRedactBodyLeavesOrdinaryBodiesUntouched(t *testing.T) {
	in := `{"user":"anne","relation":"viewer","object":"document:roadmap"}`
	if got := string(redactBody([]byte(in))); got != in {
		t.Fatalf("ordinary body altered: got %q, want %q", got, in)
	}
}

func TestCaptureRedactsTruncatedAndEscapedSecrets(t *testing.T) {
	body := `{"client_secret":"prefix\\\"still-secret`
	got := string(redactBody([]byte(body)))
	if strings.Contains(got, "still-secret") || !strings.Contains(got, `"client_secret":"******"`) {
		t.Fatalf("truncated escaped secret was not fully redacted: %q", got)
	}
}

func TestRedactURLMasksSensitiveQueryValues(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet,
		"https://api.example/stores?token=secret&continuation_token=safe", nil)
	got := redactURL(req.URL)
	if strings.Contains(got, "token=secret") || !strings.Contains(got, "token=%2A%2A%2A%2A%2A%2A") {
		t.Fatalf("redactURL() = %q", got)
	}
	if !strings.Contains(got, "continuation_token=safe") {
		t.Fatalf("redactURL removed non-sensitive query: %q", got)
	}
}

func TestRedactHeadersMasksCredentialsAndCookies(t *testing.T) {
	h := http.Header{
		"Authorization":       {"Bearer secret"},
		"Proxy-Authorization": {"test-value"},
		"Cookie":              {"test-value"},
		"Set-Cookie":          {"test-value"},
		"X-Api-Key":           {"test-value"},
		"X-Auth-Token":        {"test-value"},
		"Accept":              {"application/json"},
	}
	got := redactHeaders(h)
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-API-Key", "X-Auth-Token",
	} {
		if got.Get(name) == "test-value" || strings.Contains(got.Get(name), "secret") || got.Get(name) == "" {
			t.Errorf("%s = %q, want redacted", name, got.Get(name))
		}
	}
	if got.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q, want preserved", got.Get("Accept"))
	}
	if h.Get("Authorization") != "Bearer secret" {
		t.Fatal("redactHeaders mutated its input")
	}
}
