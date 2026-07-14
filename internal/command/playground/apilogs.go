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
	lw := apiLogListWidth(w)
	list := m.apiLogList(entries, sel, lw, h)
	e := entries[len(entries)-1-sel]
	title := e.Method + " " + e.URL
	// The status line and sub-section tab strip sit above the scrollable
	// section content; the active section (headers/body) lives in the viewport.
	card := apiLogDetailHeader(e, m.apiLogTab) + "\n\n" + m.apiLogVP.View()
	return masterDetailW(list, title, card, lw, w, h)
}

// apiLogMinDetailW keeps the detail pane wide enough for the sub-section tab
// strip even after the list pane is widened.
const apiLogMinDetailW = 40

// apiLogListWidth is the API Logs list pane width — about 25% wider than the
// shared master/detail split so long URLs have more room, clamped so the detail
// pane keeps at least apiLogMinDetailW columns.
func apiLogListWidth(w int) int {
	lw := splitListWidth(w) * 5 / 4
	if limit := w - 2 - apiLogMinDetailW; lw > limit {
		lw = limit
	}
	if lw < 1 {
		lw = 1
	}
	return lw
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
	// The latency lives in the detail pane; the list shows just the status (plus
	// a compact retry marker) so the URL gets as much room as possible.
	right := statusLabel(e)
	if e.Attempt > 1 {
		right += style.Faint.Render(fmt.Sprintf(" ×%d", e.Attempt))
	}
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

// apiLogTabs are the detail sub-sections, cycled with Tab / Shift+Tab.
var apiLogTabs = []string{"Req Headers", "Req Body", "Resp Headers", "Resp Body"}

// apiLogStatusLine renders the bold "Status:" label with the color-coded status
// text and faint timing / request-id, or the transport error for a failed call.
func apiLogStatusLine(e apilog.Entry) string {
	if e.Err != "" {
		return style.Bold.Render("Status:") + " " + style.Failure.Render("transport error")
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
	return style.Bold.Render("Status:") + " " +
		statusStyle(e.Status).Render(statusText) + "  " + style.Faint.Render(timing)
}

// apiLogTabStrip renders the sub-section labels with the active one bold and
// accented and the rest faint. It stays compact (no chip padding) so it fits
// the detail pane, wrapping to a second line only on a very narrow terminal.
func apiLogTabStrip(active int) string {
	segs := make([]string, len(apiLogTabs))
	for i, name := range apiLogTabs {
		if i == active {
			segs[i] = lipgloss.NewStyle().Bold(true).Foreground(style.Secondary).Render(name)
			continue
		}
		segs[i] = style.Faint.Render(name)
	}
	return strings.Join(segs, "  ")
}

// apiLogDetailHeader is the fixed header above the scrollable section content:
// the status line and (for a non-error entry) the sub-section tab strip.
func apiLogDetailHeader(e apilog.Entry, tab int) string {
	if e.Err != "" {
		return apiLogStatusLine(e)
	}
	return apiLogStatusLine(e) + "\n" + apiLogTabStrip(tab)
}

// apiLogSection renders the content of the active detail sub-section, with JSON
// bodies pretty-printed when pretty is true.
func apiLogSection(e apilog.Entry, pretty bool, tab int) string {
	if e.Err != "" {
		return style.Failure.Render("transport error: " + e.Err)
	}
	switch tab {
	case 1:
		return renderBody(e.ReqBody, pretty)
	case 2:
		return renderHeaders(e.RespHeaders)
	case 3:
		return renderBody(e.RespBody, pretty)
	default:
		return renderHeaders(e.ReqHeaders)
	}
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
// lazily creates the viewport, clamps the selection into range, and sizes the
// viewport to the space left below the fixed header (whose tab strip may wrap
// to a second line on a narrow pane).
func (m *Model) refreshAPILogVP() {
	w, h := m.contentSize()
	cw := w - apiLogListWidth(w) - 2
	if cw < 1 {
		cw = 1
	}

	// m.recorder is nil in tests that build a Model directly via newModel
	// instead of through Run (which always wires one up); treat that the same
	// as an empty history rather than crashing.
	var e apilog.Entry
	haveEntry := false
	if m.recorder != nil {
		entries := m.recorder.Snapshot()
		if len(entries) > 0 {
			if m.apiLogSel < 0 {
				m.apiLogSel = 0
			}
			if m.apiLogSel > len(entries)-1 {
				m.apiLogSel = len(entries) - 1
			}
			e = entries[len(entries)-1-m.apiLogSel] // newest-first index
			haveEntry = true
		}
	}

	// Reserve the master/detail title (1), the header (status line + tab strip,
	// which may wrap), and a blank separator (1) above the viewport.
	headerLines := 2
	if haveEntry {
		headerLines = lipgloss.Height(lipgloss.NewStyle().Width(cw).Render(apiLogDetailHeader(e, m.apiLogTab)))
	}
	vh := h - 2 - headerLines
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

	if !haveEntry {
		m.apiLogVP.SetContent("")
		return
	}
	m.apiLogVP.SetContent(apiLogSection(e, m.apiLogPretty, m.apiLogTab))
	// Scroll position is preserved here so an incidental re-render (a new
	// request captured while reading, or a terminal resize) doesn't snap the
	// detail back to the top. Callers that change the selected entry reset it
	// to the top explicitly via GotoTop.
}
