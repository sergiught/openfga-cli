// Package agentmode decides whether the CLI is being driven by a coding agent
// and should default to compact, prompt-free, colorless machine output.
package agentmode

import (
	"fmt"
	"strconv"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// EnvOverride is the explicit switch: 1 forces agent mode on, 0 keeps it off
// even when a coding agent is detected.
const EnvOverride = "OPENFGA_AGENT_MODE"

// detectors are variables a coding agent sets in the environment of the
// commands it runs; each is only honored when set to "1". Every entry was
// confirmed from that agent's own documentation or source:
//
//   - CLAUDECODE: set by Claude Code in its shell tool's environment.
//     https://docs.claude.com/en/docs/claude-code/settings
//   - GEMINI_CLI: "sets the GEMINI_CLI=1 environment variable in the
//     subprocess's environment" so scripts can detect they run from Gemini CLI.
//     https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/shell.md
//
// Codex's CODEX_SANDBOX / CODEX_SANDBOX_NETWORK_DISABLED were considered and
// dropped: they mark a sandbox policy, not "an agent is driving", and Codex
// documents them as experimental and subject to change
// (https://github.com/openai/codex/blob/main/codex-rs/core/src/spawn.rs).
var detectors = []string{"CLAUDECODE", "GEMINI_CLI"}

// State is the resolved agent-mode decision and why it was made.
type State struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Reason is "--agent", "--agent=false", "OPENFGA_AGENT_MODE=1",
	// "OPENFGA_AGENT_MODE=0", "<DETECTOR>=1", or "" when nothing decided.
	Reason string `json:"reason" yaml:"reason"`
}

// Resolve applies the precedence: the --agent flag (nil when not given), then
// OPENFGA_AGENT_MODE (unparsable values are ignored), then detector variables,
// then off.
func Resolve(flag *bool, getenv func(string) string) State {
	if flag != nil {
		if *flag {
			return State{Enabled: true, Reason: "--agent"}
		}
		return State{Reason: "--agent=false"}
	}
	if v := getenv(EnvOverride); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			return State{Enabled: on, Reason: EnvOverride + "=" + v}
		}
	}
	for _, name := range detectors {
		if getenv(name) == "1" {
			return State{Enabled: true, Reason: name + "=1"}
		}
	}
	return State{}
}

// EnvVars lists OPENFGA_AGENT_MODE plus every detector variable.
func EnvVars() []string {
	return append([]string{EnvOverride}, detectors...)
}

// Refuse is the usage error for an interactive feature (thing, e.g. "the
// playground") that cannot run while agent mode is on.
func (s State) Refuse(thing string) error {
	// --agent outranks the env var, so setting OPENFGA_AGENT_MODE=0 would not
	// help, and dropping --agent may still leave a detector or the env var on.
	// --agent=false overrides both.
	if s.Reason == "--agent" {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"%s is interactive and agent mode is on (--agent); pass --agent=false to use it", thing))
	}
	return clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
		"%s is interactive and agent mode is on (%s); set %s=0 to use it", thing, s.Reason, EnvOverride))
}
