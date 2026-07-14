package playground

import (
	"net/http"
	"strings"
	"testing"
	"time"

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
