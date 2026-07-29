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

// vFilterObject validates the /read filter form's object: a whole type
// ("document:") or one object ("document:roadmap"). The server's combination
// rule (object type required; object id and user not both empty) spans fields,
// so it lives in validateTupleFilter and runs at submit.
func vFilterObject(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	typ, id, ok := strings.Cut(s, ":")
	if !ok || typ == "" {
		return errors.New("must be document:roadmap (one object) or document: (a whole type)")
	}
	if strings.Contains(s, "#") {
		return errors.New("must be an object, not a userset")
	}
	// /read matches object ids literally, so a wildcard silently returns nothing.
	// vObject rejects it for the same reason; say so here too rather than let the
	// user blame an empty result on the filter.
	if id == "*" {
		return errors.New("wildcards aren't matched here — use document: to read a whole type")
	}
	return nil
}

// validateTupleFilter mirrors the server-side /read tuple_key rule (openfga
// v1.18.1, ReadQuery.Execute): the object must carry a type, and the object id
// and user cannot both be empty. The zero filter is valid — it means "clear".
//
// The object is split on its first colon exactly as the server splits it
// (tuple.SplitObject), so a colon-less object yields no type and is caught here
// rather than round-tripping into a 400. Per-field formats (the relation and
// user patterns the server's proto validation enforces) are deliberately left
// to the server; only this cross-field rule, which no single field validator
// can express, is mirrored.
func validateTupleFilter(f tupleFilter) error {
	if !f.active() {
		return nil
	}
	// Trim defensively, as every validator in this file does: a stray space would
	// otherwise make " document" look like a legitimate type.
	user := strings.TrimSpace(f.user)
	typ, id, hasColon := strings.Cut(strings.TrimSpace(f.object), ":")
	if !hasColon || typ == "" {
		return errors.New("the filter needs an object — a whole type (document:) or one object (document:roadmap)")
	}
	if id == "" && user == "" {
		return errors.New("a bare object type isn't enough — add an object id (document:roadmap) or a user")
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
