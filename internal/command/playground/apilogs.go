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
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/style"
)

// apiLogHistory caps how many requests the API Logs view retains per session.
const apiLogHistory = 200

// apiLogHStep is how many columns ←/→ shift the selected row's URL per press.
const apiLogHStep = 6

// apiLogScrollStep is how many lines j/k scroll the detail viewport per press.
const apiLogScrollStep = 3

// apiLogURLHeaderLines is how many rows the base-API-URL header above the list
// occupies (kept in sync with apiLogsBody and refreshAPILogVP's height math).
const apiLogURLHeaderLines = 1

// urlPath returns the path of a captured request URL (e.g. /stores/…), or the
// whole string when it can't be parsed. The base URL is shown once above the
// list, so rows and the detail title only need the path.
func urlPath(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return raw
}

func urlOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

// apiLogOriginLabel describes the selected entry using the URL captured with
// that request. When the active profile points at that origin its name is
// shown; entries from any other origin fall back to the bare origin.
func (m Model) apiLogOriginLabel(e apilog.Entry) string {
	origin := urlOrigin(e.URL)
	if m.cli == nil {
		return origin
	}
	name := m.cli.Config.Active
	if m.cli.Overrides.Profile != "" {
		name = m.cli.Overrides.Profile
	}
	p, ok := m.cli.Config.Get(name)
	if !ok {
		return origin
	}
	apiURL := p.APIURL
	if apiURL == "" {
		apiURL = config.DefaultAPIURL
	}
	if urlOrigin(apiURL) == origin {
		return name + " · " + origin
	}
	return origin
}

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
	return len([]rune(urlPath(entries[len(entries)-1-sel].URL)))
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
	// The base API URL is shown once above the split, so rows and the detail
	// title only carry the path; that header takes apiLogURLHeaderLines rows.
	mdH := h - apiLogURLHeaderLines
	list := m.apiLogList(entries, sel, lw, mdH)
	e := entries[len(entries)-1-sel]
	title := safeText(e.Method) + " " + safeText(urlPath(e.URL))
	// The status line and sub-section tab strip sit above the scrollable
	// section content; the active section (headers/body) lives in the viewport.
	card := apiLogDetailHeader(e, m.apiLogTab) + "\n\n" + m.apiLogVP.View()
	split := masterDetailW(list, title, card, lw, w, mdH)
	// m.cli is nil in unit tests that build a Model directly; only production
	// (via Run) has a resolved API URL to show.
	urlHeader := ""
	if m.cli != nil {
		// Indent by 2 so "API" lines up with the list rows' timestamp column
		// (rows reserve 2 columns for the selection bar / padding).
		urlHeader = "  " + style.Faint.Render("API  ") + style.Bold.Render(safeText(m.apiLogOriginLabel(e)))
	}
	return urlHeader + "\n" + split
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
	path := safeText(urlPath(e.URL))
	// The latency lives in the detail pane; the list shows just the status (plus
	// a compact retry marker) so the URL gets as much room as possible.
	// The retry marker sits before the status so the status column stays
	// right-aligned down the list regardless of whether a row retried.
	right := statusLabel(e)
	if e.Attempt > 1 {
		right = style.Faint.Render(fmt.Sprintf("×%d ", e.Attempt)) + right
	}
	prefix := style.Faint.Render(e.Time.Format("15:04:05")) + " " + safeText(e.Method) + " "

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
		timing += "  server " + safeText(e.ServerQueryDuration) + "ms"
	}
	if e.RequestID != "" {
		timing += "  req-id " + safeText(e.RequestID)
	}
	statusText := safeText(e.StatusText)
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

// apiLogTabAt maps a click position to a detail sub-section tab index. It
// returns false when the click is not on the tab strip: the wrong row, an error
// entry (which renders no strip), or a layout too narrow to show the detail
// pane. The strip sits at by+3 — the base-URL header (1), the detail section
// title (1) and the status line (1) precede it — and starts at the detail
// pane's left edge (list width + the one-column gap from masterDetailW).
func (m Model) apiLogTabAt(x, y int) (int, bool) {
	if m.recorder == nil {
		return 0, false
	}
	entries := m.recorder.Snapshot()
	if len(entries) == 0 {
		return 0, false
	}
	sel := m.apiLogSel
	if sel > len(entries)-1 {
		sel = len(entries) - 1
	}
	if sel < 0 {
		sel = 0
	}
	if entries[len(entries)-1-sel].Err != "" {
		return 0, false
	}
	w, _ := m.contentSize()
	lw := apiLogListWidth(w)
	if w-lw-2 < 10 {
		return 0, false
	}
	bx, by := m.sh.MainBodyOrigin()
	if y != by+3 {
		return 0, false
	}
	cur := bx + lw + 1
	for i, name := range apiLogTabs {
		segW := lipgloss.Width(name)
		if x >= cur && x < cur+segW {
			return i, true
		}
		cur += segW + 2 // the two-space separator between labels
	}
	return 0, false
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
		return style.Failure.Render("transport error: " + safeMultiline(e.Err))
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
		values := make([]string, len(h[k]))
		for i, value := range h[k] {
			values[i] = safeText(value)
		}
		b.WriteString(safeText(k) + ": " + strings.Join(values, ", ") + "\n")
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
			return safeMultiline(buf.String())
		}
	}
	return safeMultiline(string(raw))
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
	// Height budget: the base-URL header (apiLogURLHeaderLines), the
	// master/detail title (1), the detail header (status + tab strip), and a
	// blank separator (1) all sit above the viewport.
	vh := h - apiLogURLHeaderLines - 2 - headerLines
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
	// Soft-wrap so a long body line (especially a compact, single-line JSON
	// body) wraps to the pane instead of running off the right edge.
	m.apiLogVP.SoftWrap = true

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
