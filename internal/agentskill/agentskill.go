// Package agentskill embeds the SKILL.md that teaches a coding agent to drive
// ofga.
package agentskill

import (
	_ "embed"
	"slices"
)

//go:embed SKILL.md
var content []byte

// Content returns a copy of the embedded SKILL.md.
func Content() []byte { return slices.Clone(content) }
