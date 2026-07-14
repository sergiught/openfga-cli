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
	rt := Transport(stub, rec)

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
	rt := Transport(stub, rec)
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
	rt := Transport(stub, rec)
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
	rt := Transport(stub, rec)
	req, _ := http.NewRequest(http.MethodPost, "https://api.example/stores/1/streamed-list-objects", nil)
	rt.RoundTrip(req)
	if !strings.Contains(string(rec.Snapshot()[0].RespBody), "streamed") {
		t.Fatal("streamed endpoint response body must not be buffered")
	}
}
