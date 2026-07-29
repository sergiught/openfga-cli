package fga

import (
	"errors"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
)

// ReadFilter is the tuple_key a /read request narrows on. The zero value means
// no filter: read the whole store.
//
// It lives here so the CLI's `tuples read` and the playground's Tuples pane
// share one shape and one rule — the server's cross-field requirement is easy
// to trip and its 400 says nothing useful, so both surfaces catch it locally.
type ReadFilter struct {
	User     string
	Relation string
	Object   string
}

// NewReadFilter builds a filter from raw input, trimming each field. Both
// surfaces need this: the server's own patterns reject whitespace, so a padded
// value would pass the local check and then fail the round trip it exists to
// prevent — and a whitespace-only field must read as unset, not as a filter.
func NewReadFilter(user, relation, object string) ReadFilter {
	return ReadFilter{
		User:     strings.TrimSpace(user),
		Relation: strings.TrimSpace(relation),
		Object:   strings.TrimSpace(object),
	}
}

// Active reports whether the filter narrows anything.
func (f ReadFilter) Active() bool { return f != (ReadFilter{}) }

// TupleKey renders the filter for a ReadRequest, or nil when nothing is set —
// /read treats an all-empty tuple_key differently from an absent one.
func (f ReadFilter) TupleKey() *openfga.ReadRequestTupleKey {
	if !f.Active() {
		return nil
	}
	return &openfga.ReadRequestTupleKey{User: f.User, Relation: f.Relation, Object: f.Object}
}

// Validate mirrors the server-side /read tuple_key rule (openfga v1.18.1,
// ReadQuery.Execute): the object must carry a type, and the object id and user
// cannot both be empty. The zero filter is valid — it means "no filter".
//
// The object is split on its first colon exactly as the server splits it
// (tuple.SplitObject), so a colon-less object yields no type and is caught here
// rather than round-tripping into a 400. Per-field formats (the relation and
// user patterns the server's proto validation enforces) are deliberately left
// to the server; only this cross-field rule, which no single field validator
// can express, is mirrored.
func (f ReadFilter) Validate() error {
	if !f.Active() {
		return nil
	}
	// Trim defensively, for a filter built by hand rather than through
	// NewReadFilter: a stray space would make " document" look like a type.
	user := strings.TrimSpace(f.User)
	typ, id, hasColon := strings.Cut(strings.TrimSpace(f.Object), ":")
	if !hasColon || typ == "" {
		return ErrReadFilterNeedsObject
	}
	if id == "" && user == "" {
		return ErrReadFilterBareType
	}
	return nil
}

// The two halves of the cross-field rule, exported so each surface can phrase
// them for its own audience — a form names its fields, a command names flags.
var (
	ErrReadFilterNeedsObject = errors.New("the filter needs an object — a whole type (document:) or one object (document:roadmap)")
	ErrReadFilterBareType    = errors.New("a bare object type isn't enough — add an object id (document:roadmap) or a user")
)

// ValidateReadObject checks a /read filter's object on its own: a whole type
// ("document:") or one object ("document:roadmap"). The server's combination
// rule spans fields, so it lives in ReadFilter.Validate; this is what a form
// can check as the user types, and what a flag can check in isolation.
func ValidateReadObject(s string) error {
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
	// /read matches object ids literally, so a wildcard silently returns nothing
	// rather than "every document". Say so rather than let the user blame an
	// empty result on the filter.
	if id == "*" {
		return errors.New("wildcards aren't matched here — use document: to read a whole type")
	}
	return nil
}
