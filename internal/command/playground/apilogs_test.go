package playground

import "testing"

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
