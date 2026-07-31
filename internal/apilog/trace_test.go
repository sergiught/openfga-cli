package apilog

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTraceWriterRendersExchange(t *testing.T) {
	var b bytes.Buffer
	NewTraceWriter(&b).Add(Entry{
		Method:              "POST",
		URL:                 "http://localhost:8080/stores/01ABC/check",
		ReqHeaders:          http.Header{"Content-Type": []string{"application/json"}},
		ReqBody:             []byte(`{"tuple_key":{"user":"user:anne"}}`),
		Status:              200,
		StatusText:          "200 OK",
		RespHeaders:         http.Header{"Content-Type": []string{"application/json"}},
		RespBody:            []byte(`{"allowed":true}`),
		Elapsed:             12 * time.Millisecond,
		RequestID:           "req-123",
		ServerQueryDuration: "3ms",
	})

	got := b.String()
	for _, want := range []string{
		"> POST http://localhost:8080/stores/01ABC/check",
		">   Content-Type: application/json",
		`>   {"tuple_key":{"user":"user:anne"}}`,
		"< 200 OK  (12ms, server 3ms, request-id req-123)",
		`<   {"allowed":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace missing %q:\n%s", want, got)
		}
	}
}

// A transport failure has no status or body; the trace must still say what
// happened rather than printing an empty response block.
func TestTraceWriterRendersTransportError(t *testing.T) {
	var b bytes.Buffer
	NewTraceWriter(&b).Add(Entry{
		Method:  "GET",
		URL:     "http://localhost:9/stores",
		Err:     "dial tcp 127.0.0.1:9: connect: connection refused",
		Elapsed: 400 * time.Microsecond,
	})

	got := b.String()
	if !strings.Contains(got, "< error: dial tcp") {
		t.Errorf("trace missing the transport error:\n%s", got)
	}
	if strings.Contains(got, "< 0") {
		t.Errorf("trace rendered a bogus status for a transport failure:\n%s", got)
	}
}

// Retries are what make a slow command slow, so the attempt number has to be
// visible; a first attempt should not be cluttered with "(attempt 1)".
func TestTraceWriterMarksRetries(t *testing.T) {
	var first, retry bytes.Buffer
	NewTraceWriter(&first).Add(Entry{Method: "GET", URL: "/stores", Attempt: 1, Status: 200})
	NewTraceWriter(&retry).Add(Entry{Method: "GET", URL: "/stores", Attempt: 3, Status: 200})

	if strings.Contains(first.String(), "attempt") {
		t.Errorf("first attempt should not be annotated:\n%s", first.String())
	}
	if !strings.Contains(retry.String(), "(attempt 3)") {
		t.Errorf("retry should be annotated:\n%s", retry.String())
	}
}

// The trace prints whatever Transport hands it, and Transport redacts before
// that. This pins the end of that contract: a redacted Authorization header
// stays redacted on the way out, so --debug can never be the thing that leaks a
// token into a CI log.
func TestTraceWriterDoesNotUnmaskRedactedValues(t *testing.T) {
	var b bytes.Buffer
	NewTraceWriter(&b).Add(Entry{
		Method:     "GET",
		URL:        "http://localhost:8080/stores",
		ReqHeaders: redactHeaders(http.Header{"Authorization": []string{"Bearer super-secret-token"}}),
		Status:     200,
	})

	if strings.Contains(b.String(), "super-secret-token") {
		t.Fatalf("trace leaked a bearer token:\n%s", b.String())
	}
}

// A parallel batch-check or bulk write drives several requests at once; two
// traces interleaving mid-line would make the output unreadable.
func TestTraceWriterSerializesConcurrentEntries(t *testing.T) {
	var b bytes.Buffer
	tw := NewTraceWriter(&b)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tw.Add(Entry{Method: "GET", URL: "/stores", Status: 200, StatusText: "200 OK"})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 40 { // one request line + one response line each
		t.Fatalf("got %d lines, want 40", len(lines))
	}
	for i, line := range lines {
		wantPrefix := "> "
		if i%2 == 1 {
			wantPrefix = "< "
		}
		if !strings.HasPrefix(line, wantPrefix) {
			t.Fatalf("line %d = %q, want it to start with %q (interleaved output)", i, line, wantPrefix)
		}
	}
}
