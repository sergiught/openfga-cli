package playground

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/apilog"
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

func TestAPILogDetailToggleCompact(t *testing.T) {
	e := sampleEntry()
	pretty := apiLogDetail(e, true)
	if !strings.Contains(pretty, "{\n") {
		t.Fatalf("pretty output should contain indented JSON:\n%s", pretty)
	}
	raw := apiLogDetail(e, false)
	if !strings.Contains(raw, `{"allowed":true}`) {
		t.Fatalf("raw output should keep compact JSON:\n%s", raw)
	}
}

func TestAPILogDetailHasBlankLineBeforeBodySections(t *testing.T) {
	detail := ansi.Strip(apiLogDetail(sampleEntry(), true))
	for _, want := range []string{"\n\nRequest body", "\n\nResponse body"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("expected a blank line before %q:\n%s", strings.TrimSpace(want), detail)
		}
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
