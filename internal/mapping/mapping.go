// Package mapping builds, renders and previews openfga/mapper mapping
// documents. It owns the authoring model the `ofga mapping` wizard edits and
// the deterministic YAML it emits, and holds no terminal UI so the later
// validate/eval/test/apply subcommands can reuse it.
package mapping

import "time"

// Version is the mapping language version this package emits. The language has
// exactly one version today; mapper rejects anything else.
const Version = "1"

// Evaluation limits. The wizard previews a document after every keystroke-sized
// change, so the timeout is short enough that a runaway expression cannot lock
// up the UI, and the caps match mapper's own defaults — the point is to fail the
// same way `ofga mapping apply` eventually will, not to be permissive.
const (
	EvalTimeout      = time.Second
	MaxTuples        = 40
	MaxRules         = 100
	MaxIteratorItems = 1000
)
