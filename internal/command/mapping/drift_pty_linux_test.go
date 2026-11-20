package mapping

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

// driftAnswering runs drift with the given prompt answers waiting on a terminal.
func driftAnswering(t *testing.T, fs *fakeStore, answers string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	master, tty := openPTY(t)
	if _, werr := master.WriteString(answers); werr != nil {
		t.Fatal(werr)
	}
	return runDrift(t, fs.cli(), tty, args...)
}

func TestDriftFixMissing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	_, stderr, err := driftAnswering(t, fs, "y\n", driftArgs(t, driftMapping)...)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], "gone") {
		t.Fatalf("writes = %v", fs.writes)
	}
	for _, want := range []string{"Write 1 tuple to store acme (", "1 stale tuple left alone; run again with --prune to delete it"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestDriftFixMissingNoStaleExitsZero(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	if _, stderr, err := driftAnswering(t, fs, "y\n", driftArgs(t, driftMapping)...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 {
		t.Fatalf("writes = %v", fs.writes)
	}
}

func TestDriftDeclineWrite(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, _, err := driftAnswering(t, fs, "n\n", driftArgs(t, driftMapping)...)
	if clierr.Code(err) != clierr.CodeTestFailed || fs.writeCount() != 0 {
		t.Fatalf("code = %d writes = %d err = %v", clierr.Code(err), fs.writeCount(), err)
	}
}

func TestDriftChanged(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	stdout, stderr, err := driftAnswering(t, fs, "y\n", driftArgs(t, driftConditionMapping)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "condition differs") {
		t.Errorf("stdout:\n%s", stdout)
	}
	// The missing tuple first, then the changed one: delete, then write.
	if fs.writeCount() != 3 ||
		!strings.Contains(fs.writes[0], "user:u2") || strings.Contains(fs.writes[0], `"deletes"`) ||
		!strings.Contains(fs.writes[1], `"deletes"`) || strings.Contains(fs.writes[1], `"writes"`) ||
		!strings.Contains(fs.writes[2], `"writes"`) || !strings.Contains(fs.writes[2], "in_office") || strings.Contains(fs.writes[2], `"deletes"`) {
		t.Fatalf("writes = %v", fs.writes)
	}
	if want := "Write 1 tuple and replace 1 changed tuple (delete, then write) in store acme ("; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
}

func TestDriftChangedOnlyQuestion(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("u2"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
	_, stderr, err := driftAnswering(t, fs, "y\n", driftArgs(t, driftConditionMapping)...)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Replace 2 changed tuples (delete, then write) in store acme ("; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if fs.writeCount() != 2 {
		t.Errorf("writes = %v", fs.writes)
	}
}

// syncBuffer lets a test watch stderr while the command still writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Ctrl-C at the write question must end the run, not fall through to the
// prune question.
func TestDriftInterruptAtQuestionStops(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	master, tty := openPTY(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New(fs.cli()).Command()
	var out bytes.Buffer
	errb := &syncBuffer{}
	root.SetOut(&out)
	root.SetErr(errb)
	root.SetIn(tty)
	root.SetArgs(append([]string{"drift"}, driftArgs(t, driftMapping, "--prune")...))
	root.SilenceUsage, root.SilenceErrors = true, true
	go func() {
		for !strings.Contains(errb.String(), "[y/N]") {
			time.Sleep(time.Millisecond)
		}
		cancel()
		// The extra answer keeps a regression from hanging on a later question.
		_, _ = master.WriteString("\nn\n")
	}()
	err := root.ExecuteContext(ctx)
	if clierr.Code(err) != clierr.CodeCanceled {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if strings.Contains(errb.String(), "Delete 1 stale tuple") || fs.writeCount() != 0 {
		t.Errorf("went on after the interrupt, writes = %d:\n%s", fs.writeCount(), errb.String())
	}
}

func TestDriftPruneDryRunAsksNothing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := driftAnswering(t, fs, "y\ny\n", driftArgs(t, driftMapping, "--prune", "--dry-run")...)
	if clierr.Code(err) != clierr.CodeTestFailed || fs.writeCount() != 0 || strings.Contains(stderr, "[y/N]") {
		t.Fatalf("code = %d writes = %d err = %v\n%s", clierr.Code(err), fs.writeCount(), err, stderr)
	}
}

func TestDriftPruneOnlyAsksTheDeleteQuestion(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"), member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, err := driftAnswering(t, fs, "y\n", driftArgs(t, driftMapping, "--prune")...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if strings.Count(stderr, "[y/N]") != 1 || !strings.Contains(stderr, "Delete 1 stale tuple from store acme (") || strings.Contains(stderr, "Write ") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestDriftPrune(t *testing.T) {
	for _, tc := range []struct {
		name, answers string
		wantWrites    int
		wantDelete    bool
		wantCode      int
	}{
		{"both yes", "y\ny\n", 2, true, 0},
		{"write only", "y\nn\n", 1, false, clierr.CodeTestFailed},
		{"delete only", "n\ny\n", 1, true, clierr.CodeTestFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t, member("gone"))
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			_, stderr, err := driftAnswering(t, fs, tc.answers, driftArgs(t, driftMapping, "--prune")...)
			if clierr.Code(err) != tc.wantCode {
				t.Fatalf("code = %d err = %v", clierr.Code(err), err)
			}
			if fs.writeCount() != tc.wantWrites {
				t.Fatalf("writes = %v", fs.writes)
			}
			hasDelete := false
			for _, w := range fs.writes {
				hasDelete = hasDelete || strings.Contains(w, `"deletes"`)
			}
			if hasDelete != tc.wantDelete {
				t.Errorf("deleted = %v, want %v: %v", hasDelete, tc.wantDelete, fs.writes)
			}
			if !strings.Contains(stderr, "Delete 1 stale tuple from store acme (") {
				t.Errorf("stderr:\n%s", stderr)
			}
		})
	}
}

func TestDriftAgeLine(t *testing.T) {
	configtest.Isolate(t)
	pinNow(t, 6*time.Minute)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	_, stderr, _ := driftAnswering(t, fs, "y\ny\n", driftArgs(t, driftMapping, "--prune")...)
	const age = "Snapshot is 6m old; changes since then are not reflected."
	if strings.Count(stderr, age) != 2 {
		t.Fatalf("want the age line twice:\n%s", stderr)
	}
	first := strings.Index(stderr, age)
	if q := strings.Index(stderr, "Write 1 tuple"); first > q {
		t.Errorf("age line must precede the write question:\n%s", stderr)
	}
	if q := strings.Index(stderr, "Delete 1 stale"); strings.LastIndex(stderr, age) > q {
		t.Errorf("age line must precede the delete question:\n%s", stderr)
	}
}

func TestDriftNoInputOnTerminalReportsOnly(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("gone"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	master, tty := openPTY(t)
	if _, err := master.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	c := fs.cli()
	c.NoInput = true
	_, stderr, err := runDrift(t, c, tty, driftArgs(t, driftMapping)...)
	if clierr.Code(err) != clierr.CodeTestFailed || !clierr.IsSilent(err) {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(stderr, "report only: pass --force to write") || strings.Contains(stderr, "[y/N]") || fs.writeCount() != 0 {
		t.Errorf("writes = %d\n%s", fs.writeCount(), stderr)
	}
}
