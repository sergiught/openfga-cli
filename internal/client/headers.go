package client

import (
	"fmt"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
)

// reservedHeaders are headers a caller must not set through --header.
//
// Authorization is the important one: the SDK composes auth *above* the static
// header layer and its header transport skips any header the request already
// carries, so `--header 'Authorization: ...'` on an authenticated profile is
// silently dropped. Failing loudly beats a credential that looks configured and
// never leaves the process. The rest are set by the transport itself and would
// be equally ignored.
var reservedHeaders = map[string]string{
	"Authorization":  "use a profile's auth settings (`ofga profiles set token|client_secret|…`) or --auth-token-file",
	"Content-Type":   "set by the API client",
	"Content-Length": "set by the HTTP transport",
	"Host":           "set from --api-url",
}

// ParseHeaders turns repeated "Name: value" strings into an http.Header.
//
// A later value for the same name replaces an earlier one, so a --header flag
// overrides the same header from the profile rather than sending both.
func ParseHeaders(raw []string) (http.Header, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	h := make(http.Header, len(raw))
	for _, s := range raw {
		name, value, ok := strings.Cut(s, ":")
		if !ok {
			return nil, fmt.Errorf("header %q must be in Name: value form", s)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return nil, fmt.Errorf("header %q has an empty name", s)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if hint, reserved := reservedHeaders[canonical]; reserved {
			return nil, fmt.Errorf("header %q cannot be set with --header: %s", canonical, hint)
		}
		// A newline in either half would let one flag inject additional headers
		// into the request.
		if strings.ContainsAny(name+value, "\r\n") {
			return nil, fmt.Errorf("header %q must not contain newlines", s)
		}
		if !validHeaderName(name) {
			return nil, fmt.Errorf("header name %q contains characters that are not allowed", name)
		}
		h.Set(canonical, value)
	}
	return h, nil
}

// headerNames lists the canonical names in h, sorted so the result is stable.
// The API-log capture masks these values: a header the user configured is there
// to authenticate against a gateway, so it holds a credential.
func headerNames(h http.Header) []string {
	if len(h) == 0 {
		return nil
	}
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validHeaderName reports whether name is a valid RFC 7230 field-name (a
// token). net/http would silently mangle or reject anything else.
func validHeaderName(name string) bool {
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			return false
		}
	}
	return true
}
