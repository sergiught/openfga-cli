package playground

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/style"
)

// apiLogHistory caps how many requests the API Logs view retains per session.
const apiLogHistory = 200

// apiLogMsg is sent (via Recorder.SetNotify -> program.Send) whenever a new
// request is captured, so the API Logs view re-renders.
type apiLogMsg struct{}

// apiLogsBody renders the API Logs section: a master list of requests
// (newest first) alongside a detail pane for the current selection.
func (m Model) apiLogsBody() string {
	// m.recorder is nil in tests that build a Model directly via newModel
	// instead of through Run (which always wires one up); treat that the
	// same as an empty history rather than crashing.
	if m.recorder == nil {
		return style.Faint.Render("No API calls yet.")
	}
	entries := m.recorder.Snapshot()
	if len(entries) == 0 {
		return style.Faint.Render("No API calls yet.")
	}
	w, h := m.contentSize()
	sel := m.apiLogSel
	if sel > len(entries)-1 {
		sel = len(entries) - 1
	}
	list := m.apiLogList(entries, sel, h)
	e := entries[len(entries)-1-sel]
	title := e.Method + " " + e.URL
	return masterDetail(list, title, m.apiLogVP.View(), w, h)
}

// apiLogList renders compact request rows, newest first, windowed to fit h
// lines around the current selection.
func (m Model) apiLogList(entries []apilog.Entry, sel, h int) string {
	if h < 1 {
		h = 1
	}
	n := len(entries)
	start := 0
	if n > h {
		start = sel - h/2
		if start < 0 {
			start = 0
		}
		if start > n-h {
			start = n - h
		}
	}
	end := start + h
	if end > n {
		end = n
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		row := apiLogRow(entries[n-1-i])
		if i == sel {
			b.WriteString(style.Bold.Render("▸ ") + row)
		} else {
			b.WriteString("  " + row)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// apiLogRow renders a single-line summary of a request: time, method, path,
// status, latency, and retry count.
func apiLogRow(e apilog.Entry) string {
	path := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Path != "" {
		path = u.Path
	}
	attempts := ""
	if e.Attempt > 1 {
		attempts = fmt.Sprintf(" (%d attempts)", e.Attempt)
	}
	return fmt.Sprintf("%s %s %s %s %dms%s",
		e.Time.Format("15:04:05"), e.Method, path, statusLabel(e), e.Elapsed.Milliseconds(), attempts)
}

// statusLabel colors status code by class, or shows ERR on transport error.
func statusLabel(e apilog.Entry) string {
	if e.Err != "" {
		return style.Failure.Render("ERR")
	}
	s := fmt.Sprintf("%d", e.Status)
	switch {
	case e.Status >= 500:
		return style.Failure.Render(s)
	case e.Status >= 400:
		return style.Warn.Render(s)
	default:
		return style.Success.Render(s)
	}
}

// apiLogDetail renders the full request/response detail pane for e, with
// bodies pretty-printed as indented JSON when pretty is true, or shown
// exactly as captured otherwise.
func apiLogDetail(e apilog.Entry, pretty bool) string {
	var b strings.Builder
	b.WriteString(style.Bold.Render(e.Method+" "+e.URL) + "\n")
	if e.Err != "" {
		b.WriteString(style.Failure.Render("transport error: "+e.Err) + "\n")
		return strings.TrimRight(b.String(), "\n")
	}
	meta := fmt.Sprintf("Status: %s  %dms", e.StatusText, e.Elapsed.Milliseconds())
	if e.ServerQueryDuration != "" {
		meta += "  server " + e.ServerQueryDuration + "ms"
	}
	if e.RequestID != "" {
		meta += "  req-id " + e.RequestID
	}
	b.WriteString(style.Faint.Render(meta) + "\n\n")

	b.WriteString(style.Bold.Render("Request headers") + "\n" + renderHeaders(e.ReqHeaders) + "\n")
	b.WriteString(style.Bold.Render("Request body") + "\n" + renderBody(e.ReqBody, pretty) + "\n\n")
	b.WriteString(style.Bold.Render("Response headers") + "\n" + renderHeaders(e.RespHeaders) + "\n")
	b.WriteString(style.Bold.Render("Response body") + "\n" + renderBody(e.RespBody, pretty))
	return strings.TrimRight(b.String(), "\n")
}

// renderHeaders formats an http.Header as sorted "Key: value" lines.
func renderHeaders(h http.Header) string {
	if len(h) == 0 {
		return style.Faint.Render("(none)")
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + ": " + strings.Join(h[k], ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderBody formats a captured request/response body: indented JSON when
// pretty is true and the body parses as JSON, or the raw bytes otherwise.
func renderBody(raw []byte, pretty bool) string {
	if len(raw) == 0 {
		return style.Faint.Render("(empty)")
	}
	if pretty {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err == nil {
			return buf.String()
		}
	}
	return string(raw)
}

// refreshAPILogVP rebuilds the detail viewport for the current selection. It
// lazily creates the viewport and clamps the selection into range.
func (m *Model) refreshAPILogVP() {
	w, h := m.contentSize()
	cw := w - splitListWidth(w) - 2
	if cw < 1 {
		cw = 1
	}
	vh := h - 2
	if vh < 1 {
		vh = 1
	}
	if !m.apiLogVPInit {
		m.apiLogVP = viewport.New(viewport.WithWidth(cw), viewport.WithHeight(vh))
		m.apiLogVPInit = true
	} else {
		m.apiLogVP.SetWidth(cw)
		m.apiLogVP.SetHeight(vh)
	}
	// m.recorder is nil in tests that build a Model directly via newModel
	// instead of through Run (which always wires one up); treat that the same
	// as an empty history rather than crashing.
	if m.recorder == nil {
		m.apiLogVP.SetContent("")
		return
	}
	entries := m.recorder.Snapshot()
	if len(entries) == 0 {
		m.apiLogVP.SetContent("")
		return
	}
	if m.apiLogSel < 0 {
		m.apiLogSel = 0
	}
	if m.apiLogSel > len(entries)-1 {
		m.apiLogSel = len(entries) - 1
	}
	e := entries[len(entries)-1-m.apiLogSel] // newest-first index
	m.apiLogVP.SetContent(apiLogDetail(e, m.apiLogPretty))
	m.apiLogVP.GotoTop()
}
