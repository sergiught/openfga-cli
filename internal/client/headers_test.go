package client

import "testing"

func TestParseHeaders(t *testing.T) {
	h, err := ParseHeaders([]string{"X-Scope-OrgID: tenant-7", "x-tenant:acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Get("X-Scope-Orgid"); got != "tenant-7" {
		t.Errorf("X-Scope-OrgID = %q, want tenant-7", got)
	}
	// Name is canonicalized, and surrounding whitespace on the value is dropped.
	if got := h.Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant = %q, want acme", got)
	}
	if got, err := ParseHeaders(nil); got != nil || err != nil {
		t.Errorf("ParseHeaders(nil) = %v, %v; want nil, nil", got, err)
	}
}

// A later value replaces an earlier one so a --header flag overrides the same
// header from the profile instead of sending it twice.
func TestParseHeadersLastValueWins(t *testing.T) {
	h, err := ParseHeaders([]string{"X-Tenant: from-profile", "X-Tenant: from-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Values("X-Tenant"); len(got) != 1 || got[0] != "from-flag" {
		t.Errorf("X-Tenant = %v, want exactly [from-flag]", got)
	}
}

// Authorization through --header is silently dropped by the SDK when a profile
// has auth, so it must be rejected rather than looking configured.
func TestParseHeadersRejectsReservedAndMalformed(t *testing.T) {
	for _, tt := range []struct{ name, in string }{
		{"authorization", "Authorization: Bearer x"},
		{"authorization lowercase", "authorization: Bearer x"},
		{"content-type", "Content-Type: text/plain"},
		{"host", "Host: evil.example"},
		{"no colon", "X-Tenant"},
		{"empty name", ": value"},
		{"newline injection", "X-A: b\r\nX-Injected: c"},
		{"invalid name", "X Tenant: v"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseHeaders([]string{tt.in}); err == nil {
				t.Fatalf("ParseHeaders(%q) should have failed", tt.in)
			}
		})
	}
}
