package cli

import "testing"

func TestRedactedURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://fga.example.com", "https://fga.example.com"},
		{"https://svc@fga.example.com", "https://svc@fga.example.com"},
		{"https://svc:s3cr3t@fga.example.com", "https://svc:xxxxx@fga.example.com"},
		// url.Parse rejects this, but `profiles set api_url` stores it as-is.
		{"https://svc:s3cr3t@fga.example.com/%zz", "<malformed URL hidden>"},
		{"not a url %zz", "not a url %zz"},
	} {
		if got := RedactedURL(tc.in); got != tc.want {
			t.Errorf("RedactedURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
