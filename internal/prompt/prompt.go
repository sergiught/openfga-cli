// Package prompt provides consistent confirmation gates for destructive
// commands: interactive y/N (or type-to-confirm) on a terminal, and a required
// --force flag when running non-interactively so scripts fail safe.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// ErrAborted is returned when the user declines a confirmation.
var ErrAborted = errors.New("aborted")

// interactive reports whether cmd's stdin is a terminal we can prompt on.
func interactive(cmd *cobra.Command) bool {
	if noInput, err := cmd.Flags().GetBool("no-input"); err == nil && noInput {
		return false
	}
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// Confirm gates a destructive action with a y/N question. force (from --force)
// approves it unconditionally. Otherwise it prompts on a TTY (defaulting to
// no); with no TTY and no --force it refuses so piped/CI runs fail safe.
func Confirm(cmd *cobra.Command, question string, force bool) error {
	if force {
		return nil
	}
	if !interactive(cmd) {
		return errors.New("refusing to proceed without confirmation; pass --force to skip the prompt")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", question)
	switch readLine(cmd) {
	case "y", "yes":
		return nil
	default:
		return ErrAborted
	}
}

// ConfirmName gates a severe action (e.g. deleting a store and all its data)
// behind typing the resource's exact name. force skips it; a non-TTY without
// --force refuses.
func ConfirmName(cmd *cobra.Command, action, expected string, force bool) error {
	if force {
		return nil
	}
	if !interactive(cmd) {
		return errors.New("refusing to proceed without confirmation; pass --force to skip the prompt")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s\n  type %q to confirm: ", action, expected)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if strings.TrimSpace(line) == expected {
		return nil
	}
	return ErrAborted
}

func readLine(cmd *cobra.Command) string {
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line))
}

// Ask prompts on a TTY for a line of text, showing def as the default. It
// returns def when the user presses enter on an empty line, or immediately (no
// prompt) when stdin is not a terminal — so non-interactive runs use defaults.
func Ask(cmd *cobra.Command, question, def string) string {
	if !interactive(cmd) {
		return def
	}
	if def != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", question)
	}
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if v := strings.TrimSpace(line); v != "" {
		return v
	}
	return def
}

// AskSecret prompts on a TTY for a secret without echoing it (via
// term.ReadPassword), so tokens never appear on screen or in scrollback. It
// returns "" when stdin is not a terminal — non-interactive runs should supply
// secrets via flags or stdin, not this prompt.
func AskSecret(cmd *cobra.Command, question string) string {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return ""
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", question)
	b, err := term.ReadPassword(f.Fd())
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
