// Package snapshot reads an identity provider's current state as events a
// mapping file can replay, and decides which mapping rules opt in to that.
package snapshot

import (
	"context"
	"time"
)

// Type is one kind of snapshot event a provider can produce.
type Type struct {
	Name        string   // "snapshot.organization.member"
	Mirrors     string   // "organization.member.added"
	Description string   // one line
	Scopes      []string // provider permissions the read needs
}

// Count is how many events of one type a snapshot produced.
type Count struct {
	Type string
	N    int
}

// Result is a completed snapshot read.
type Result struct {
	Source                string // what was read, for display: the Auth0 domain
	Events                []map[string]any
	Counts                []Count // one per requested type, in the provider's order
	StartedAt, FinishedAt time.Time
}

// Provider reads one identity provider's current state.
type Provider interface {
	Name() string  // "auth0", the --provider value
	Label() string // "Auth0", for display
	Types() []Type
	// Snapshot reads the given types completely or returns an error.
	// Events are ordered parents before children; the core replays them as given.
	Snapshot(ctx context.Context, types []string, progress func(msg string)) (*Result, error)
}
