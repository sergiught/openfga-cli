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
