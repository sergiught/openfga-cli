package apilog

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// TraceWriter is a Sink that formats each entry to a writer as it completes,
// for `ofga --debug`. Unlike Recorder it keeps nothing: the point is to show
// the exchange while the command is still running, so a hung or slow request is
// visible before the process exits.
//
// Everything it prints has already been through Transport's redaction
// (Authorization and cookie headers, secret-named JSON/form fields, URL query
// values), and Transport only ever hands it OpenFGA API traffic — OAuth
// token-fetch requests, which carry the client secret and the access token, are
// never captured in the first place.
type TraceWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTraceWriter returns a Sink that writes a human-readable trace to w.
func NewTraceWriter(w io.Writer) *TraceWriter {
	return &TraceWriter{w: w}
}

// Add renders one exchange. Concurrent callers (a parallel batch-check, a bulk
// tuple write) are serialized so two traces never interleave mid-line.
func (t *TraceWriter) Add(e Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	req := fmt.Sprintf("> %s %s", e.Method, e.URL)
	if e.Attempt > 1 {
		req += fmt.Sprintf("  (attempt %d)", e.Attempt)
	}
	b.WriteString(req + "\n")
	writeHeaders(&b, ">", e.ReqHeaders)
	writeBody(&b, ">", e.ReqBody)

	switch {
	case e.Err != "":
		fmt.Fprintf(&b, "< error: %s  (%s)\n", e.Err, roundDuration(e.Elapsed))
	default:
		status := e.StatusText
		if status == "" {
			status = fmt.Sprint(e.Status)
		}
		fmt.Fprintf(&b, "< %s  (%s", status, roundDuration(e.Elapsed))
		if e.ServerQueryDuration != "" {
			fmt.Fprintf(&b, ", server %s", e.ServerQueryDuration)
		}
		if e.RequestID != "" {
			fmt.Fprintf(&b, ", request-id %s", e.RequestID)
		}
		b.WriteString(")\n")
		writeHeaders(&b, "<", e.RespHeaders)
		writeBody(&b, "<", e.RespBody)
	}

	_, _ = io.WriteString(t.w, b.String())
}

// writeHeaders prints headers in a stable order so two runs of the same command
// produce diffable traces.
func writeHeaders(b *strings.Builder, arrow string, h http.Header) {
	if len(h) == 0 {
		return
	}
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, v := range h[name] {
			fmt.Fprintf(b, "%s   %s: %s\n", arrow, name, v)
		}
	}
}

// writeBody prints a captured body, indented to line up with the headers.
// Bodies are already capped by Transport, so this cannot print unboundedly.
func writeBody(b *strings.Builder, arrow string, body []byte) {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return
	}
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(b, "%s   %s\n", arrow, line)
	}
}

// roundDuration keeps timings readable: microseconds for sub-millisecond
// exchanges, milliseconds otherwise.
func roundDuration(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return d.Round(time.Microsecond)
	}
	return d.Round(time.Millisecond)
}
