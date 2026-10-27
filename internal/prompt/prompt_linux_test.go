package prompt

import (
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// openPTY returns a pseudo-terminal pair so a test can give a command a real
// terminal on stdin, the one case the strings.Reader helper cannot cover.
func openPTY(t *testing.T) (master, tty *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	tty, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open pts: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	return master, tty
}

// --no-input (which agent mode sets) must stop the secret prompt even when
// stdin is a terminal, as it does for every other prompt.
func TestNoInputDisablesSecretPromptOnTerminal(t *testing.T) {
	master, tty := openPTY(t)
	if _, err := master.WriteString("s3cret\n"); err != nil {
		t.Fatal(err)
	}
	cmd, errBuf := newCmd("")
	cmd.SetIn(tty)
	cmd.Flags().Bool("no-input", false, "")
	if err := cmd.Flags().Set("no-input", "true"); err != nil {
		t.Fatal(err)
	}
	if got := AskSecret(cmd, "API token"); got != "" {
		t.Fatalf("AskSecret() = %q, want no prompt under --no-input", got)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("prompted under --no-input: %q", errBuf.String())
	}
}
