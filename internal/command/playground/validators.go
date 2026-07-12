package playground

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/sergiught/openfga-cli/internal/fga"
)

// Field validators used for inline (on-blur) form validation. Each is lenient
// on an empty value — required-ness is enforced at submit — so navigating an
// empty field never nags; only a non-empty, malformed value is flagged.

func vUser(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	if !strings.Contains(s, ":") {
		return errors.New("must be type:id (e.g. user:anne)")
	}
	return nil
}

func vObject(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	typ, id := fga.SplitObject(s)
	if typ == "" || id == "" {
		return errors.New("must be type:id (e.g. document:roadmap)")
	}
	if id == "*" || strings.Contains(s, "#") {
		return errors.New("must be a concrete type:id")
	}
	return nil
}

func vJSON(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	if !json.Valid([]byte(s)) {
		return errors.New("must be valid JSON")
	}
	return nil
}

func vURL(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("must be an http(s) URL")
	}
	return nil
}
