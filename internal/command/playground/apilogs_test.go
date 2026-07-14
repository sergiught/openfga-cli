package playground

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

func TestAPILogsSectionRegistered(t *testing.T) {
	if int(secAPILogs) != int(secAssertions)+1 {
		t.Fatalf("secAPILogs must follow secAssertions, got %d", secAPILogs)
	}
	if len(sectionNames) != int(secAPILogs)+1 {
		t.Fatalf("sectionNames has %d entries, want %d", len(sectionNames), int(secAPILogs)+1)
	}
	if sectionNames[secAPILogs] != "API Logs" {
		t.Fatalf("sectionNames[secAPILogs] = %q", sectionNames[secAPILogs])
	}
}

// apiLogModel builds a Model wired with a recorder pre-loaded with entries,
// on the API Logs section, with its detail viewport refreshed.
func apiLogModel(entries ...apilog.Entry) Model {
	sh := shell.New()
	sh.SetSize(120, 30)
	rec := apilog.NewRecorder(apiLogHistory)
	for _, e := range entries {
		rec.Add(e)
	}
	m := Model{sh: sh, recorder: rec, apiLogPretty: true, section: secAPILogs}
	m.refreshAPILogVP()
	return m
}

func sampleEntry() apilog.Entry {
	h := http.Header{}
	h.Set("Authorization", "Bearer ***redacted***")
	return apilog.Entry{
		Time:        time.Date(2026, 7, 14, 14, 22, 1, 0, time.UTC),
		Method:      "POST",
		URL:         "https://api.example/stores/1/check",
		ReqHeaders:  h,
		ReqBody:     []byte(`{"tuple_key":{"user":"user:1"}}`),
		Status:      200,
		StatusText:  "200 OK",
		RespHeaders: h,
		RespBody:    []byte(`{"allowed":true}`),
		Elapsed:     18 * time.Millisecond,
		RequestID:   "req-123",
	}
}

func TestAPILogsEmptyState(t *testing.T) {
	m := apiLogModel()
	body := m.apiLogsBody()
	if !strings.Contains(body, "No API calls yet") {
		t.Fatalf("expected empty-state message, got:\n%s", body)
	}
}

func TestAPILogsBodyShowsRowAndDetail(t *testing.T) {
	m := apiLogModel(sampleEntry())
	body := m.apiLogsBody()
	for _, want := range []string{"POST", "/stores/1/check", "200"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestAPILogSectionBodyToggleCompact(t *testing.T) {
	e := sampleEntry()
	pretty := apiLogSection(e, true, 3) // Resp Body tab
	if !strings.Contains(pretty, "{\n") {
		t.Fatalf("pretty output should contain indented JSON:\n%s", pretty)
	}
	raw := apiLogSection(e, false, 3)
	if !strings.Contains(raw, `{"allowed":true}`) {
		t.Fatalf("raw output should keep compact JSON:\n%s", raw)
	}
}

func TestAPILogDetailHeaderShowsTabStrip(t *testing.T) {
	header := ansi.Strip(apiLogDetailHeader(sampleEntry(), 0))
	for _, want := range apiLogTabs {
		if !strings.Contains(header, want) {
			t.Fatalf("detail header should list sub-section tab %q:\n%s", want, header)
		}
	}
}

func TestAPILogTabCyclesSections(t *testing.T) {
	m := apiLogModel(sampleEntry())
	if m.apiLogTab != 0 {
		t.Fatalf("default tab should be 0, got %d", m.apiLogTab)
	}
	m = pressAPILog(m, "tab")
	if m.apiLogTab != 1 {
		t.Fatalf("tab should advance to 1, got %d", m.apiLogTab)
	}
	m = pressAPILog(m, "shift+tab")
	m = pressAPILog(m, "shift+tab")
	if m.apiLogTab != len(apiLogTabs)-1 {
		t.Fatalf("shift+tab should wrap to the last tab, got %d", m.apiLogTab)
	}
}

func TestAPILogTabSwitchesViewportContent(t *testing.T) {
	m := apiLogModel(sampleEntry())
	if v := m.apiLogVP.View(); !strings.Contains(v, "Authorization") {
		t.Fatalf("Req Headers tab should show request headers:\n%s", v)
	}
	m = pressAPILog(m, "tab") // Req Body
	m = pressAPILog(m, "tab") // Resp Headers
	m = pressAPILog(m, "tab") // Resp Body
	if v := m.apiLogVP.View(); !strings.Contains(v, "allowed") {
		t.Fatalf("Resp Body tab should show the response body:\n%s", v)
	}
}

func TestAPILogsBodyMarksSelectedRow(t *testing.T) {
	// The selected row carries a thick left bar (lipgloss ThickBorder) so it is
	// unambiguous which entry is selected.
	m := apiLogModel(sampleEntry(), sampleEntry())
	if body := m.apiLogsBody(); !strings.Contains(body, "┃") {
		t.Fatalf("expected the selected row to be marked with a bar:\n%s", body)
	}
}

func TestStatusStyleByClass(t *testing.T) {
	if statusStyle(200).Render("x") != style.Success.Render("x") {
		t.Error("2xx should use the Success (green) style")
	}
	if statusStyle(301).Render("x") != style.Success.Render("x") {
		t.Error("3xx should use the Success style")
	}
	if statusStyle(404).Render("x") != style.Warn.Render("x") {
		t.Error("4xx should use the Warn style")
	}
	if statusStyle(500).Render("x") != style.Failure.Render("x") {
		t.Error("5xx should use the Failure style")
	}
}

func TestAPILogStatusLine(t *testing.T) {
	// The status label is bold and the status text is present and color-coded
	// (green for 2xx) — assert the structure on the ANSI-stripped output.
	line := ansi.Strip(apiLogStatusLine(sampleEntry()))
	if !strings.Contains(line, "Status: 200 OK") {
		t.Fatalf("expected a 'Status: 200 OK' line:\n%s", line)
	}
}

func TestAPILogRightArrowScrollsSelectedURL(t *testing.T) {
	sh := shell.New()
	sh.SetSize(80, 20)
	rec := apilog.NewRecorder(apiLogHistory)
	rec.Add(apilog.Entry{
		Method: "POST", Status: 200, StatusText: "200 OK",
		URL: "https://api.example.com/stores/01/a/very/long/path/that/is/truncated/in/the/list/check",
	})
	m := Model{sh: sh, recorder: rec, apiLogPretty: true, section: secAPILogs}
	m.refreshAPILogVP()

	before := ansi.Strip(m.apiLogList(rec.Snapshot(), 0, 50, 3))
	m = pressAPILog(m, "right")
	if m.apiLogHScroll == 0 {
		t.Fatal("right arrow should increase the horizontal scroll offset")
	}
	if after := ansi.Strip(m.apiLogList(rec.Snapshot(), 0, 50, 3)); after == before {
		t.Fatalf("the selected URL should shift on horizontal scroll:\n%q\n%q", before, after)
	}
	// left arrow clamps at 0.
	m = pressAPILog(m, "left")
	m = pressAPILog(m, "left")
	if m.apiLogHScroll != 0 {
		t.Fatalf("left arrow should clamp the offset at 0, got %d", m.apiLogHScroll)
	}
}

func TestAPILogDetailTitleUsesPathNotFullURL(t *testing.T) {
	rec := apilog.NewRecorder(apiLogHistory)
	rec.Add(apilog.Entry{Method: "POST", URL: "https://api.example.com/stores/1/check", Status: 200, StatusText: "200 OK"})
	sh := shell.New()
	sh.SetSize(120, 20)
	m := Model{sh: sh, recorder: rec, apiLogPretty: true, section: secAPILogs}
	m.refreshAPILogVP()
	body := ansi.Strip(m.apiLogsBody())
	if !strings.Contains(body, "/stores/1/check") {
		t.Fatalf("expected the path in the detail:\n%s", body)
	}
	if strings.Contains(body, "https://") {
		t.Fatalf("full URL should not appear (the base URL is a separate header):\n%s", body)
	}
}

func TestAPILogJKScrollDetail(t *testing.T) {
	sh := shell.New()
	sh.SetSize(80, 8)
	rec := apilog.NewRecorder(apiLogHistory)
	big := `{"objects":[` + strings.Repeat(`"document:xxxxxxxxxxxxxxxxxxxx",`, 60) + `"end"]}`
	rec.Add(apilog.Entry{
		Method: "POST", URL: "https://api.example/list", Status: 200, StatusText: "200 OK",
		RespBody: []byte(big),
	})
	m := Model{sh: sh, recorder: rec, apiLogPretty: true, section: secAPILogs}
	m.refreshAPILogVP()
	m = pressAPILog(m, "tab")
	m = pressAPILog(m, "tab")
	m = pressAPILog(m, "tab") // Resp Body (overflows)
	if m.apiLogVP.TotalLineCount() <= m.apiLogVP.Height() {
		t.Skip("content fits; nothing to scroll")
	}
	m = pressAPILog(m, "k") // scroll down
	off := m.apiLogVP.YOffset()
	if off == 0 {
		t.Fatal("k should scroll the detail down")
	}
	m = pressAPILog(m, "j") // scroll up
	if m.apiLogVP.YOffset() >= off {
		t.Fatal("j should scroll the detail up")
	}
}

func TestAPILogPageDownScrollsDetail(t *testing.T) {
	sh := shell.New()
	sh.SetSize(80, 8) // small height so the section overflows the viewport
	rec := apilog.NewRecorder(apiLogHistory)
	big := `{"objects":[` + strings.Repeat(`"document:xxxxxxxxxxxxxxxxxxxx",`, 60) + `"end"]}`
	rec.Add(apilog.Entry{
		Method: "POST", URL: "https://api.example/list", Status: 200, StatusText: "200 OK",
		RespBody: []byte(big),
	})
	m := Model{sh: sh, recorder: rec, apiLogPretty: true, section: secAPILogs}
	m.refreshAPILogVP()
	// Move to the Resp Body tab, whose big pretty-printed body overflows.
	m = pressAPILog(m, "tab")
	m = pressAPILog(m, "tab")
	m = pressAPILog(m, "tab")
	if m.apiLogVP.TotalLineCount() <= m.apiLogVP.Height() {
		t.Skip("content fits in the viewport; nothing to scroll")
	}
	m = pressAPILog(m, "pgdown")
	if m.apiLogVP.YOffset() == 0 {
		t.Fatal("pgdown should scroll the detail viewport")
	}
}

func pressAPILog(m Model, key string) Model {
	nm, _ := m.handleSectionKey(key, tea.KeyPressMsg{})
	return nm.(Model)
}

func TestAPILogKeysNavigateSelectToggleClear(t *testing.T) {
	m := apiLogModel(sampleEntry(), sampleEntry()) // two entries; sel starts at 0 (newest)

	// down moves toward older entries (sel++)
	m = pressAPILog(m, "down")
	if m.apiLogSel != 1 {
		t.Fatalf("down: apiLogSel = %d, want 1", m.apiLogSel)
	}
	// up moves back toward newest (sel--)
	m = pressAPILog(m, "up")
	if m.apiLogSel != 0 {
		t.Fatalf("up: apiLogSel = %d, want 0", m.apiLogSel)
	}
	// down cannot pass the oldest
	m = pressAPILog(m, "down")
	m = pressAPILog(m, "down")
	if m.apiLogSel != 1 {
		t.Fatalf("down clamp: apiLogSel = %d, want 1", m.apiLogSel)
	}
	// c toggles pretty
	before := m.apiLogPretty
	m = pressAPILog(m, "c")
	if m.apiLogPretty == before {
		t.Fatal("c should toggle apiLogPretty")
	}
	// x clears history
	m = pressAPILog(m, "x")
	if len(m.recorder.Snapshot()) != 0 {
		t.Fatal("x should clear the recorder")
	}
	if m.apiLogSel != 0 {
		t.Fatalf("x should reset selection, got %d", m.apiLogSel)
	}
}

func TestAPILogKeysNilRecorderNoPanic(t *testing.T) {
	sh := shell.New()
	sh.SetSize(120, 30)
	m := Model{sh: sh, section: secAPILogs} // no recorder wired

	// A nil recorder must not panic on any recorder-touching key.
	for _, key := range []string{"x", "down", "up", "c"} {
		nm, _ := m.handleSectionKey(key, tea.KeyPressMsg{})
		if nm == nil {
			t.Fatalf("handleSectionKey(%q) returned nil model", key)
		}
	}
}
