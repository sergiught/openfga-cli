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
	Windowed    bool     // the read can be limited to entities changed since a time
}

// Count is how many events of one type a snapshot produced.
type Count struct {
	Type     string
	N        int
	Windowed bool // this read was limited by Request.Since
}

// Request is what one snapshot read asks for.
type Request struct {
	Types []string
	Since time.Time // zero: everything. Otherwise Windowed types return only entities changed at or after it.
	// Emit, when set, is given the events instead of Result.Events: in the
	// same order, each event once, never from two goroutines at once, as soon
	// as that order allows. An error from Emit ends the read and is returned
	// as it is. A provider may ignore Emit and fill Result.Events.
	Emit func(events []map[string]any) error
}

// Result is a completed snapshot read.
type Result struct {
	Source                string           // what was read, for display: the Auth0 domain
	Events                []map[string]any // every event, in order; nil when the provider gave them to Request.Emit
	Counts                []Count          // one per requested type, in the provider's order
	RateLimited           int              // responses of this read that were HTTP 429
	StartedAt, FinishedAt time.Time
}

// Progress is one report of a read in progress.
type Progress struct {
	Message     string // the whole line, printed when there is no live status; empty for a report only a live status shows
	Step        string // what is being read, for a live status: "members of organizations"; empty for a notice that is not a step
	Done, Total int    // of Step; Total is 0 while the size is not known
}

// Stats is what the read in progress has done so far.
type Stats struct {
	Requests    int           // HTTP requests sent, every resend after a 429 included
	RateLimited int           // answers that were HTTP 429
	Pace        time.Duration // spacing currently kept between requests, 0 when not holding back
	Counts      []Count       // items read so far: one per requested type, in the provider's order
}

// Provider reads one identity provider's current state.
type Provider interface {
	Name() string  // "auth0", the --provider value
	Label() string // "Auth0", for display
	Types() []Type
	// Snapshot reads the requested types completely or returns an error.
	// Events are ordered parents before children; the core replays them as given.
	Snapshot(ctx context.Context, req Request, progress func(Progress)) (*Result, error)
	// Stats says what the read in progress has done so far. It is safe to call
	// from another goroutine while Snapshot runs; after Snapshot returns it
	// describes that read. Before any read it returns the zero value.
	Stats() Stats
}
