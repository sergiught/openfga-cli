package prompt

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newCmd returns a cobra command whose stdin is the given text and whose
// stderr is captured. Because the stdin is not an *os.File, interactive()
// reports false — exercising the non-TTY path.
func newCmd(stdin string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(stdin))
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&errBuf)
	return cmd, &errBuf
}

func TestConfirmForceSkips(t *testing.T) {
	cmd, _ := newCmd("")
	if err := Confirm(cmd, "delete it", true); err != nil {
		t.Errorf("Confirm(force) = %v, want nil", err)
	}
}

func TestConfirmNonInteractiveRefuses(t *testing.T) {
	cmd, _ := newCmd("y\n") // stdin is not a TTY
	err := Confirm(cmd, "delete it", false)
	if err == nil || errors.Is(err, ErrAborted) {
		t.Fatalf("Confirm(non-tty) = %v, want a refuse-without-force error", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refuse error should mention --force: %v", err)
	}
}

func TestConfirmNameForceSkips(t *testing.T) {
	cmd, _ := newCmd("")
	if err := ConfirmName(cmd, "delete store X", "X", true); err != nil {
		t.Errorf("ConfirmName(force) = %v, want nil", err)
	}
}

func TestConfirmNameNonInteractiveRefuses(t *testing.T) {
	cmd, _ := newCmd("X\n")
	if err := ConfirmName(cmd, "delete store X", "X", false); err == nil {
		t.Error("ConfirmName(non-tty) should refuse without --force")
	}
}
