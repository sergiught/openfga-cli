// Package mapping builds, renders and previews openfga/mapper mapping
// documents. It owns the authoring model the `ofga mappings` workspace edits and
// the deterministic YAML it emits, and holds no terminal UI so the validate,
// test and apply subcommands can reuse it.
package mapping

import "time"

// Version is the mapping language version this package emits. The language has
// exactly one version today; mapper rejects anything else.
const Version = "1"

// Evaluation limits. The caps match mapper's own defaults, so the workspace fails
// the same way `ofga mappings apply` does rather than being permissive. EvalTimeout is the workspace's alone: it previews the document after
// every keystroke-sized change, so it is short enough that a runaway expression
// cannot lock up the UI. validate, test and apply use mapper's DefaultTimeout,
// as `fga` does.
const (
	EvalTimeout      = time.Second
	MaxTuples        = 40
	MaxRules         = 100
	MaxIteratorItems = 1000
)
