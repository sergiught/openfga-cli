package mapping

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
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

// chunks is a writer that keeps each Write call as its own chunk.
type chunks struct {
	mu   sync.Mutex
	list []string
}

func (c *chunks) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, string(p))
	return len(p), nil
}

func (c *chunks) text() string {
	var b strings.Builder
	for _, ch := range c.list {
		b.WriteString(ch)
	}
	return ansi.Strip(b.String())
}

// isCard reports whether a chunk is a card frame or the erase of one: both end
// by clearing to the end of the screen.
func isCard(chunk string) bool { return strings.HasSuffix(chunk, "\x1b[J") }

// indexOf is the first chunk at or after from whose text contains s, or -1.
func (c *chunks) indexOf(from int, s string) int {
	for i := from; i < len(c.list); i++ {
		if strings.Contains(ansi.Strip(c.list[i]), s) {
			return i
		}
	}
	return -1
}

// firstCard is the first card chunk at or after from, or -1.
func (c *chunks) firstCard(from int) int {
	for i := from; i < len(c.list); i++ {
		if isCard(c.list[i]) {
			return i
		}
	}
	return -1
}

// onlyCard fails unless every chunk in [from, to) is a card frame or erase:
// nothing else may be written while the card draws.
func (c *chunks) onlyCard(t *testing.T, from, to int) {
	t.Helper()
	if from < 0 || to < from {
		t.Fatalf("no stretch to check: chunks %d to %d", from, to)
	}
	for i := from; i < to; i++ {
		if !isCard(c.list[i]) {
			t.Errorf("chunk %d was written while the card drew: %q", i, c.list[i])
		}
	}
}

func (c *chunks) noCardAfter(t *testing.T, i int) {
	t.Helper()
	if j := c.firstCard(i + 1); j >= 0 {
		t.Errorf("a card frame was drawn after chunk %d: chunk %d", i, j)
	}
}

// runTogether runs a snapshot command with stdout and stderr sharing one
// writer, so the order of everything it prints can be asserted.
func runTogether(t *testing.T, c *cli.CLI, stdin io.Reader, name string, args ...string) (*chunks, error) {
	t.Helper()
	root := New(c).Command()
	w := &chunks{}
	root.SetOut(w)
	root.SetErr(w)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetArgs(append([]string{name}, args...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err := root.ExecuteContext(context.Background())
	return w, err
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

// legacyLine matches the progress lines a card replaces. "replaying event"
// only prints from progressEvery events on, which these runs do not reach.
var legacyLine = regexp.MustCompile(`reading the store|reading fake|reading \d+ objects? in the store|sent \d+/\d+|replaying event`)

func noLegacy(t *testing.T, out string) {
	t.Helper()
	if m := legacyLine.FindString(out); m != "" {
		t.Errorf("legacy progress line %q printed while the card was live:\n%s", m, out)
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
	w, err := runTogether(t, fs.cli(), nil, "backfill", mp, "--provider", "fake", "--force")
	out := w.text()
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	inOrder(t, out, "ofga mappings backfill", "read in ", "Fake  ", "Store  ", "ofga mappings backfill", "wrote in ", "wrote 4 tuples to store acme")
	noLegacy(t, out)
	read := w.indexOf(0, "read in ")
	w.onlyCard(t, w.firstCard(0), read)
	wrote := w.indexOf(read, "wrote in ")
	w.onlyCard(t, w.firstCard(read+1), wrote)
	w.noCardAfter(t, wrote)
}

func TestDriftDrawsTheCardInsteadOfProgressLines(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	w, err := runTogether(t, fs.cli(), nil, "drift", driftArgs(t, driftMapping, "--prune", "--force")...)
	out := w.text()
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	inOrder(t, out, "ofga mappings drift", "read in ", "Store  ", "ofga mappings drift", "wrote in ", "sent 1 write to store acme",
		"ofga mappings drift", "pruned in ", "deleted 1 stale tuple from store acme")
	noLegacy(t, out)
	read := w.indexOf(0, "read in ")
	w.onlyCard(t, w.firstCard(0), read)
	wrote := w.indexOf(read, "wrote in ")
	w.onlyCard(t, w.firstCard(read+1), wrote)
	pruned := w.indexOf(wrote, "pruned in ")
	w.onlyCard(t, w.firstCard(wrote+1), pruned)
	w.noCardAfter(t, pruned)
}

func TestBackfillProviderFailureStopsTheCardFirst(t *testing.T) {
	configtest.Isolate(t)
	useCard(t)
	fs := newFakeStore(t)
	boom := errors.New("provider exploded")
	useFakeProvider(t, fakeProvider{types: fakeTypes(), err: boom})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	w, err := runTogether(t, fs.cli(), nil, "backfill", mp, "--provider", "fake", "--force")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	stopped := w.indexOf(0, "stopped after ")
	if stopped < 0 || stopped != len(w.list)-1 {
		t.Fatalf("want the stopped line last, nothing about the error before or after it:\n%s", w.text())
	}
	w.onlyCard(t, w.firstCard(0), stopped)
	noLegacy(t, w.text())
}

func TestBackfillWriteFailureSummarisesBeforeTheFailureText(t *testing.T) {
	configtest.Isolate(t)
	useCard(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	w, err := runTogether(t, fs.cli(), nil, "backfill", mp, "--provider", "fake", "--force")
	if err == nil {
		t.Fatal("want a write failure")
	}
	read := w.indexOf(0, "read in ")
	wrote := w.indexOf(read, "wrote in ")
	failure := w.indexOf(wrote, "before the failure")
	if read < 0 || wrote < 0 || failure < 0 {
		t.Fatalf("want the read, wrote and failure lines in order:\n%s", w.text())
	}
	w.onlyCard(t, w.firstCard(read+1), wrote)
	w.noCardAfter(t, wrote)
}

func TestDryRunHasOneLiveStretchAndNoWritePhase(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			useCard(t)
			fs := newFakeStore(t, member("gone"))
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			args := driftArgs(t, driftMapping, "--dry-run")
			if name == "backfill" {
				args = []string{writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping), "--provider", "fake", "--dry-run"}
			}
			w, _ := runTogether(t, fs.cli(), nil, name, args...)
			out := w.text()
			if n := strings.Count(out, "read in "); n != 1 {
				t.Errorf("%d live stretches, want 1:\n%s", n, out)
			}
			for _, ch := range w.list {
				if isCard(ch) && strings.Contains(ansi.Strip(ch), "Write") {
					t.Fatalf("a frame names the Write phase in a dry run:\n%s", ansi.Strip(ch))
				}
			}
			if strings.Contains(out, "wrote in ") {
				t.Errorf("a dry run wrote:\n%s", out)
			}
			w.noCardAfter(t, w.indexOf(0, "read in "))
		})
	}
}

func TestNoCardForMachineOutput(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			asked := useCard(t)
			fs := newFakeStore(t)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			c := fs.cli()
			c.JSON = true
			args := driftArgs(t, driftMapping, "--dry-run")
			if name == "backfill" {
				args = []string{writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping), "--provider", "fake", "--dry-run"}
			}
			w, _ := runTogether(t, c, nil, name, args...)
			if *asked != 0 || strings.Contains(w.text(), "ofga mappings") {
				t.Errorf("a card was built (%d times) or drawn:\n%s", *asked, w.text())
			}
		})
	}
}

// Tracing is checked on the constructor's gate and, for backfill, through the
// command with a read that fails before the store is read: a traced POST body
// races inside the request tracer, which this test is not about.
func TestNoCardUnderTracing(t *testing.T) {
	configtest.Isolate(t)
	asked := useCard(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), err: errors.New("stop here")})
	c := fs.cli()
	lg := log.New(io.Discard)
	lg.SetLevel(log.DebugLevel)
	c.Logger, c.TraceWriter = lg, io.Discard
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	w, _ := runTogether(t, c, nil, "backfill", mp, "--provider", "fake", "--dry-run")
	if *asked != 0 || strings.Contains(w.text(), "ofga mappings") {
		t.Errorf("a card was built (%d times) or drawn:\n%s", *asked, w.text())
	}
	if (&Command{cli: c}).liveStatus(io.Discard, false, "t") != nil || *asked != 0 {
		t.Error("liveStatus built a card under tracing")
	}
}

func TestDriftSinceUsersOnlyDeclaresNoPruneStep(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	pinNow(t, 0)
	fs := newFakeStore(t, map[string]any{"user": "user:gone", "relation": "member", "object": "tenant:t1"})
	useFakeProvider(t, fakeProvider{types: windowedTypes(), result: windowedResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", userSnapshotMapping)
	w, err := runTogether(t, fs.cli(), nil, "drift", mp, "--provider", "fake", "--since", "2h", "--prune", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, w.text())
	}
	frames := 0
	for _, ch := range w.list {
		if !isCard(ch) {
			continue
		}
		frames++
		text := ansi.Strip(ch)
		if strings.Contains(text, "Prune") {
			t.Fatalf("a frame names Prune, which cannot run here:\n%s", text)
		}
		if strings.Contains(text, "Write") {
			inOrder(t, text, "Fake", "Store", "Write")
		}
	}
	if frames == 0 {
		t.Fatal("no frame drawn")
	}
}

func TestDriftWriteFailureSummarisesBeforeTheFailureText(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	w, err := runTogether(t, fs.cli(), nil, "drift", driftArgs(t, driftMapping, "--force")...)
	if err == nil {
		t.Fatal("want a write failure")
	}
	read := w.indexOf(0, "read in ")
	wrote := w.indexOf(read, "wrote in ")
	failure := w.indexOf(wrote, "before the failure")
	if read < 0 || wrote < 0 || failure < 0 {
		t.Fatalf("want the read, wrote and failure lines in order:\n%s", w.text())
	}
	w.onlyCard(t, w.firstCard(read+1), wrote)
	w.noCardAfter(t, wrote)
}
