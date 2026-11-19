package mapping

import (
	"errors"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// openPTY returns a pseudo-terminal pair, so a command's confirmation prompt
// finds a terminal on stdin.
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

func TestBackfillAsksAndDeclines(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 6*time.Minute)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	master, tty := openPTY(t)
	if _, err := master.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runBackfill(t, fs.cli(), tty, mp, "--provider", "fake")
	if !errors.Is(err, prompt.ErrAborted) {
		t.Fatalf("err = %v", err)
	}
	age := strings.Index(stderr, "Snapshot is 6m old; changes since then are not reflected.")
	ask := strings.Index(stderr, "Write 4 tuples to store acme (")
	if age < 0 || ask < 0 || age > ask {
		t.Errorf("age line must precede the question:\n%s", stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote after a no")
	}
}
