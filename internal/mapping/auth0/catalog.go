// Package auth0 carries a catalog of Auth0 Events event types and an example
// payload for each, embedded so `ofga mapping init` can preview a rule without
// network access. See README.md for provenance.
package auth0

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed samples/*.json
var samples embed.FS

//go:embed model.fga
var model string

// Model is an authorization model every recipe in this catalog lints against:
// one file covering all twenty-one events rather than a fragment per event.
//
// The recipes each declare the slice of it they need, which is what the recipe
// screen shows you. This is the other half of that — the union, in a form you
// can write to disk and hand to `ofga model write`.
func Model() string { return model }

// Event is one catalog entry: the event type, the group it is filed under in
// the picker, a one-line summary, and the decoded example payload.
type Event struct {
	Type    string
	Group   string
	Summary string
	Sample  map[string]any
	Recipe  Recipe
}

// catalog is ordered as the picker renders it: grouped, alphabetical within a
// group, with the groups ordered by how often they carry authorization data.
var catalog = []struct{ typ, group, summary string }{
	{"user.created", "User", "A user was created"},
	{"user.deleted", "User", "A user was deleted"},
	{"user.updated", "User", "A user's profile changed"},

	{"organization.connection.added", "Organization", "A connection was associated with an organization"},
	{"organization.connection.removed", "Organization", "A connection was dissociated from an organization"},
	{"organization.connection.updated", "Organization", "An organization's connection settings changed"},
	{"organization.created", "Organization", "An organization was created"},
	{"organization.deleted", "Organization", "An organization was deleted"},
	{"organization.member.added", "Organization", "A user joined an organization"},
	{"organization.member.deleted", "Organization", "A user left an organization"},
	{"organization.member.role.assigned", "Organization", "A member was granted a role"},
	{"organization.member.role.deleted", "Organization", "A member's role was revoked"},
	{"organization.updated", "Organization", "An organization's metadata changed"},

	{"group.created", "Group", "A group was created"},
	{"group.deleted", "Group", "A group was deleted"},
	{"group.member.added", "Group", "A user joined a group"},
	{"group.member.deleted", "Group", "A user left a group"},
	{"group.updated", "Group", "A group's metadata changed"},

	{"connection.created", "Connection", "A connection was created"},
	{"connection.deleted", "Connection", "A connection was deleted"},
	{"connection.updated", "Connection", "A connection's settings changed"},
}

// Catalog returns every known event type, in picker order. Samples are decoded
// fresh on each call so a caller holding one cannot affect another's.
func Catalog() []Event {
	out := make([]Event, 0, len(catalog))
	for _, e := range catalog {
		sample, err := loadSample(e.typ)
		if err != nil {
			// Unreachable in a built binary: the samples are embedded and a
			// missing or malformed one fails catalog_test.go first.
			panic(fmt.Sprintf("auth0: %v", err))
		}
		out = append(out, Event{Type: e.typ, Group: e.group, Summary: e.summary, Sample: sample, Recipe: recipeFor(e.typ)})
	}
	return out
}

// Lookup returns the catalog entry for typ.
func Lookup(typ string) (Event, bool) {
	for _, e := range Catalog() {
		if e.Type == typ {
			return e, true
		}
	}
	return Event{}, false
}

func loadSample(typ string) (map[string]any, error) {
	raw, err := samples.ReadFile("samples/" + typ + ".json")
	if err != nil {
		return nil, fmt.Errorf("read sample for %s: %w", typ, err)
	}
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("decode sample for %s: %w", typ, err)
	}
	return ev, nil
}
