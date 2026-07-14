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
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/style"
)

// apiLogHistory caps how many requests the API Logs view retains per session.
const apiLogHistory = 200

// apiLogHStep is how many columns ←/→ shift the selected row's URL per press.
const apiLogHStep = 6

// apiLogMsg is sent (via Recorder.SetNotify -> program.Send) whenever a new
// request is captured, so the API Logs view re-renders.
type apiLogMsg struct{}

// selectedAPILogPathLen returns the rune length of the selected entry's URL
// path, used to clamp horizontal scrolling so it can't run past the URL's end.
func (m Model) selectedAPILogPathLen(entries []apilog.Entry) int {
	if len(entries) == 0 {
		return 0
	}
	sel := m.apiLogSel
	if sel < 0 {
		sel = 0
	}
	if sel > len(entries)-1 {
		sel = len(entries) - 1
	}
	e := entries[len(entries)-1-sel]
	path := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Path != "" {
		path = u.Path
	}
	return len([]rune(path))
}

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
	if sel < 0 {
		sel = 0
	}
	list := m.apiLogList(entries, sel, splitListWidth(w), h)
	e := entries[len(entries)-1-sel]
	title := e.Method + " " + e.URL
	return masterDetail(list, title, m.apiLogVP.View(), w, h)
}

// apiLogRowSelected marks the current row with a thick left bar (mirroring the
// list component's selection treatment); apiLogRowNormal indents other rows to
// match, so the columns stay aligned whether or not a row is selected.
var (
	apiLogRowSelected = lipgloss.NewStyle().
				Border(lipgloss.ThickBorder(), false, false, false, true).
				BorderForeground(style.Secondary).
				Padding(0, 0, 0, 1)
	apiLogRowNormal = lipgloss.NewStyle().Padding(0, 0, 0, 2)
)

// apiLogList renders compact request rows, newest first, windowed to fit h
// lines around the current selection. width is the list pane width.
func (m Model) apiLogList(entries []apilog.Entry, sel, width, h int) string {
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
	// Both row styles spend 2 columns on the bar/padding, so lay out the row
	// content in the remaining width.
	cw := width - 2
	if cw < 1 {
		cw = 1
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		// Only the selected row scrolls horizontally, so other rows stay
		// anchored at the start of their path and remain readable.
		hs := 0
		if i == sel {
			hs = m.apiLogHScroll
		}
		row := apiLogRow(entries[n-1-i], cw, hs)
		if i == sel {
			b.WriteString(apiLogRowSelected.Render(row))
		} else {
			b.WriteString(apiLogRowNormal.Render(row))
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// apiLogRow renders one compact row within width: a faint timestamp and method
// on the left, then the request path, then the status and latency right-aligned
// so the columns line up down the list. hscroll shifts the path left so a long
// URL can be read in full with the ←/→ keys.
func apiLogRow(e apilog.Entry, width, hscroll int) string {
	path := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Path != "" {
		path = u.Path
	}
	lat := fmt.Sprintf("%dms", e.Elapsed.Milliseconds())
	if e.Attempt > 1 {
		lat += fmt.Sprintf("×%d", e.Attempt)
	}
	// Right-align the latency in a fixed field so the status codes (always 3
	// columns) and latencies line up as columns down the list.
	if pad := 8 - len(lat); pad > 0 {
		lat = strings.Repeat(" ", pad) + lat
	}
	right := statusLabel(e) + " " + style.Faint.Render(lat)
	prefix := style.Faint.Render(e.Time.Format("15:04:05")) + " " + e.Method + " "

	avail := width - lipgloss.Width(right) - 1
	if avail < 1 {
		avail = 1
	}
	pathW := avail - lipgloss.Width(prefix)
	if pathW < 1 {
		pathW = 1
	}
	pr := []rune(path)
	if hscroll > len(pr) {
		hscroll = len(pr)
	}
	if hscroll > 0 {
		pr = pr[hscroll:]
	}
	shown := ansi.Truncate(string(pr), pathW, "…")
	if pad := pathW - lipgloss.Width(shown); pad > 0 {
		shown += strings.Repeat(" ", pad)
	}
	return prefix + shown + " " + right
}

// statusLabel colors the status code by class, or shows ERR on a transport
// error.
func statusLabel(e apilog.Entry) string {
	if e.Err != "" {
		return style.Failure.Render("ERR")
	}
	return statusStyle(e.Status).Render(fmt.Sprintf("%d", e.Status))
}

// statusStyle returns the color treatment for an HTTP status class: green for
// 2xx/3xx, amber for 4xx, red for 5xx.
func statusStyle(status int) lipgloss.Style {
	switch {
	case status >= 500:
		return style.Failure
	case status >= 400:
		return style.Warn
	default:
		return style.Success
	}
}

// apiLogDetail renders the full request/response detail pane for e, with
// bodies pretty-printed as indented JSON when pretty is true, or shown
// exactly as captured otherwise.
func apiLogDetail(e apilog.Entry, pretty bool) string {
	var b strings.Builder
	if e.Err != "" {
		b.WriteString(style.Failure.Render("transport error: "+e.Err) + "\n")
		return strings.TrimRight(b.String(), "\n")
	}
	timing := fmt.Sprintf("%dms", e.Elapsed.Milliseconds())
	if e.ServerQueryDuration != "" {
		timing += "  server " + e.ServerQueryDuration + "ms"
	}
	if e.RequestID != "" {
		timing += "  req-id " + e.RequestID
	}
	statusText := e.StatusText
	if statusText == "" {
		statusText = fmt.Sprintf("%d", e.Status)
	}
	b.WriteString(style.Bold.Render("Status:") + " " +
		statusStyle(e.Status).Render(statusText) + "  " + style.Faint.Render(timing) + "\n\n")

	b.WriteString(style.Bold.Render("Request headers") + "\n" + renderHeaders(e.ReqHeaders) + "\n\n")
	b.WriteString(style.Bold.Render("Request body") + "\n" + renderBody(e.ReqBody, pretty) + "\n\n")
	b.WriteString(style.Bold.Render("Response headers") + "\n" + renderHeaders(e.RespHeaders) + "\n\n")
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
	// Scroll position is preserved here so an incidental re-render (a new
	// request captured while reading, or a terminal resize) doesn't snap the
	// detail back to the top. Callers that change the selected entry reset it
	// to the top explicitly via GotoTop.
}
