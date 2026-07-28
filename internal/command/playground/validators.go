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

// vFilterObject validates the /read filter form's object: a full type:id or a
// bare type ("document:"). The server's combination rule (object type
// required; object id and user not both empty) spans fields, so it lives in
// validateTupleFilter and runs at submit.
func vFilterObject(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	if typ, _, ok := strings.Cut(s, ":"); !ok || typ == "" {
		return errors.New("must be type:id or type: (e.g. document:roadmap or document:)")
	}
	if strings.Contains(s, "#") {
		return errors.New("must be an object, not a userset")
	}
	return nil
}

// validateTupleFilter mirrors the server-side /read tuple_key rule (openfga
// ReadQuery.Execute): the object must carry a type, and the object id and user
// cannot both be empty. The zero filter is valid — it means "clear".
//
// The object is split on its first colon exactly as the server splits it, so a
// colon-less object yields no type and is rejected here rather than round-
// tripping into a 400.
func validateTupleFilter(f tupleFilter) error {
	if !f.active() {
		return nil
	}
	typ, id, hasColon := strings.Cut(f.object, ":")
	if !hasColon || typ == "" {
		return errors.New("filtering needs an object type — set object to type: or type:id (e.g. document: or document:roadmap)")
	}
	if id == "" && f.user == "" {
		return errors.New("an object type alone is too broad — add an object id (document:roadmap) or a user")
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
