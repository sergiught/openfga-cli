package mapping

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/command/mapping/status"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

// useCard makes the commands draw their status card on whatever writer they
// are given, at a fixed size, and returns how often a card was asked for.
func useCard(t *testing.T) *int {
	t.Helper()
	old, asked := newStatus, new(int)
	newStatus = func(w io.Writer, title string, phases ...string) *status.Live {
		*asked++
		return status.NewAt(w, 80, 24, title, phases...)
	}
	t.Cleanup(func() { newStatus = old })
	return asked
}

// runTogether runs a snapshot command with stdout and stderr sharing one
// buffer, so the order of everything it prints can be asserted.
func runTogether(t *testing.T, c *cli.CLI, name string, args ...string) (string, error) {
	t.Helper()
	root := New(c).Command()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{name}, args...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err := root.ExecuteContext(context.Background())
	return ansi.Strip(buf.String()), err
}

// inOrder fails unless each text occurs after the one before it.
func inOrder(t *testing.T, out string, texts ...string) {
	t.Helper()
	at := 0
	for _, text := range texts {
		i := strings.Index(out[at:], text)
		if i < 0 {
			t.Fatalf("%q not found after offset %d, want the order %q:\n%s", text, at, texts, out)
		}
		at += i + len(text)
	}
}

func TestBackfillDrawsTheCardInsteadOfProgressLines(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	pinNow(t, 60e9)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	out, err := runTogether(t, fs.cli(), "backfill", mp, "--provider", "fake", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	inOrder(t, out, "ofga mappings backfill", "read in ", "Fake read", "store read", "ofga mappings backfill", "wrote in ", "wrote 4 tuples to store acme")
	for _, legacy := range []string{"reading the store", "replaying event"} {
		if strings.Contains(out, legacy) {
			t.Errorf("legacy progress line %q printed while the card was live:\n%s", legacy, out)
		}
	}
}

func TestDriftDrawsTheCardInsteadOfProgressLines(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	out, err := runTogether(t, fs.cli(), "drift", driftArgs(t, driftMapping, "--prune", "--force")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	inOrder(t, out, "ofga mappings drift", "read in ", "store read", "ofga mappings drift", "wrote in ", "sent 1 write to store acme",
		"ofga mappings drift", "pruned in ", "deleted 1 stale tuple from store acme")
	for _, legacy := range []string{"reading the store", "replaying event"} {
		if strings.Contains(out, legacy) {
			t.Errorf("legacy progress line %q printed while the card was live:\n%s", legacy, out)
		}
	}
}

func TestNoCardForMachineOutputOrTracing(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		for _, mode := range []string{"json", "tracing"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				configtest.Isolate(t)
				asked := useCard(t)
				fs := newFakeStore(t)
				useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
				c := fs.cli()
				if mode == "json" {
					c.JSON = true
				} else {
					lg := log.New(io.Discard)
					lg.SetLevel(log.DebugLevel)
					c.Logger, c.TraceWriter = lg, io.Discard
				}
				args := driftArgs(t, driftMapping, "--dry-run")
				if name == "backfill" {
					args = []string{writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping), "--provider", "fake", "--dry-run"}
				}
				out, _ := runTogether(t, c, name, args...)
				if *asked != 0 || strings.Contains(out, "ofga mappings") {
					t.Errorf("a card was built (%d times) or drawn:\n%s", *asked, out)
				}
			})
		}
	}
}
