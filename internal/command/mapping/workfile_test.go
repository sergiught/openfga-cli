package mapping

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
)

func keptLine(path string) string {
	return "work file kept at " + path + "; run again with --resume to continue"
}

// stateArgs is one command's arguments with its mapping file and a work file path.
func stateArgs(t *testing.T, mapping, state string, extra ...string) []string {
	t.Helper()
	args := []string{writeFile(t, t.TempDir(), "m.yaml", mapping), "--provider", "fake", "--state", state}
	return append(args, extra...)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// useCacheDir points the default work file location at a fresh directory.
func useCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := cacheDir
	cacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { cacheDir = old })
	return dir
}

func TestWorkFileDeletedAfterAForcedRun(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			state := filepath.Join(t.TempDir(), "w.db")
			_, stderr, err := runSnapshotCmd(t, fs.cli(), nil, name, stateArgs(t, driftMapping, state, "--force")...)
			if err != nil {
				t.Fatalf("err = %v\n%s", err, stderr)
			}
			if fs.writeCount() == 0 {
				t.Fatal("nothing was written")
			}
			if exists(state) {
				t.Errorf("the work file is still there")
			}
			if strings.Contains(stderr, "work file kept") {
				t.Errorf("stderr:\n%s", stderr)
			}
		})
	}
}

func TestKeepStateKeepsIt(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			state := filepath.Join(t.TempDir(), "w.db")
			_, stderr, err := runSnapshotCmd(t, fs.cli(), nil, name, stateArgs(t, driftMapping, state, "--force", "--keep-state")...)
			if err != nil {
				t.Fatalf("err = %v\n%s", err, stderr)
			}
			info, serr := os.Stat(state)
			if serr != nil {
				t.Fatalf("the work file is gone: %v", serr)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 0600", info.Mode().Perm())
			}
		})
	}
}

func TestResumeIsNotAvailableYet(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	state := filepath.Join(t.TempDir(), "w.db")
	_, _, err := runBackfill(t, fs.cli(), nil, stateArgs(t, driftMapping, state, "--resume")...)
	if err == nil || exists(state) {
		t.Fatalf("err = %v, file exists = %v", err, exists(state))
	}
}

func TestFailureKeepsTheFileAndHints(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name+" write failure", func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			fs.failWrite = `{"code":"validation_error","message":"type not found"}`
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
			state := filepath.Join(t.TempDir(), "w.db")
			_, stderr, err := runSnapshotCmd(t, fs.cli(), nil, name, stateArgs(t, driftMapping, state, "--force")...)
			if err == nil {
				t.Fatal("want a write failure")
			}
			if !exists(state) {
				t.Fatal("the work file was deleted")
			}
			if strings.Count(stderr, keptLine(state)) != 1 {
				t.Errorf("want the hint once:\n%s", stderr)
			}
		})
		t.Run(name+" cancelled", func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1"),
				hook: func(snapshot.Request) error { cancel(); return ctx.Err() }})
			state := filepath.Join(t.TempDir(), "w.db")
			root := New(fs.cli()).Command()
			var out, errb bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errb)
			root.SetArgs(append([]string{name}, stateArgs(t, driftMapping, state, "--force")...))
			root.SilenceUsage, root.SilenceErrors = true, true
			if err := root.ExecuteContext(ctx); err == nil {
				t.Fatal("want an error")
			}
			if !exists(state) {
				t.Fatal("the work file was deleted")
			}
			if got := ansi.Strip(errb.String()); strings.Count(got, keptLine(state)) != 1 {
				t.Errorf("want the hint once:\n%s", got)
			}
		})
	}
}

func TestFailureUnderMachineOutputPutsTheHintInTheError(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	state := filepath.Join(t.TempDir(), "w.db")
	c := fs.cli()
	c.JSON = true
	_, stderr, err := runBackfill(t, c, nil, stateArgs(t, driftMapping, state, "--force")...)
	if err == nil || !exists(state) {
		t.Fatalf("err = %v, file exists = %v", err, exists(state))
	}
	if stderr != "" {
		t.Errorf("stderr is for the error envelope alone:\n%s", stderr)
	}
	if hint := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(hint, keptLine(state)) {
		t.Errorf("hint = %q", hint)
	}
}

func TestExistingFileIsReplacedWithANotice(t *testing.T) {
	configtest.Isolate(t)
	dir := useCacheDir(t)
	pinNow(t, 60e9)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	args := driftArgs(t, driftMapping, "--force", "--keep-state")
	if _, stderr, err := runBackfill(t, fs.cli(), nil, args...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	found, _ := filepath.Glob(filepath.Join(dir, "ofga", "mappings", "*.db"))
	if len(found) != 1 {
		t.Fatalf("work files = %v", found)
	}
	_, stderr, err := runBackfill(t, fs.cli(), nil, args...)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	want := "discarding the unfinished run from 2026-11-19 14:03 UTC at " + found[0]
	if strings.Count(stderr, "discarding") != 1 || !strings.Contains(stderr, want) {
		t.Errorf("want one line %q:\n%s", want, stderr)
	}
	if !exists(found[0]) {
		t.Error("the new run's file is gone")
	}
}

func TestDefaultStateUnderCacheDir(t *testing.T) {
	configtest.Isolate(t)
	dir := useCacheDir(t)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	args := driftArgs(t, driftMapping, "--force", "--keep-state")
	if _, stderr, err := runDrift(t, fs.cli(), nil, args...); err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	found, _ := filepath.Glob(filepath.Join(dir, "ofga", "mappings", "m-*.db"))
	if len(found) != 1 {
		t.Fatalf("work files = %v", found)
	}
	info, err := os.Stat(filepath.Dir(found[0]))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory = %v, %v", info, err)
	}
}

func TestNoCacheDirNeedsState(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			old := cacheDir
			cacheDir = func() (string, error) { return "", errors.New("no home") }
			t.Cleanup(func() { cacheDir = old })
			fs := newFakeStore(t)
			var calls int
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1"), calls: &calls})
			_, _, err := runSnapshotCmd(t, fs.cli(), nil, name, driftArgs(t, driftMapping, "--force")...)
			if err == nil {
				t.Fatal("want an error")
			}
			if hint := clierr.NewEnvelope(err).Error.Hint; hint != "pass --state FILE to choose where the work file goes" {
				t.Errorf("hint = %q", hint)
			}
			if calls != 0 || len(fs.readRequests()) != 0 || fs.writeCount() != 0 {
				t.Errorf("provider reads = %d, store reads = %d, writes = %d", calls, len(fs.readRequests()), fs.writeCount())
			}
		})
	}
}

// A provider whose Save fails stands for a work file that cannot be written.
func TestUnwritableWorkFileFailsBeforeWriting(t *testing.T) {
	for _, name := range []string{"backfill", "drift"} {
		t.Run(name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1"),
				hook: func(req snapshot.Request) error {
					return req.Save(snapshot.Unit{Key: "x"}, []map[string]any{{"type": "snapshot.not.a.type"}}, true)
				}})
			state := filepath.Join(t.TempDir(), "w.db")
			_, stderr, err := runSnapshotCmd(t, fs.cli(), nil, name, stateArgs(t, driftMapping, state, "--force")...)
			if err == nil || !strings.Contains(err.Error(), state) {
				t.Fatalf("err = %v, want it to name %s", err, state)
			}
			if fs.writeCount() != 0 {
				t.Errorf("writes = %v", fs.writes)
			}
			if !strings.Contains(stderr, keptLine(state)) {
				t.Errorf("stderr:\n%s", stderr)
			}
		})
	}
}

// A report's list length is the count taken when the diff was made: a work file
// that fails afterwards must not turn into an empty list.
func TestCountedLenNeverAsksTheFile(t *testing.T) {
	f, err := workfile.Create(filepath.Join(t.TempDir(), "w.db"), workfile.Meta{Command: "backfill"})
	if err != nil {
		t.Fatal(err)
	}
	l := counted{f.Desired(), 3}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if l.Len() != 3 {
		t.Errorf("Len = %d, want 3", l.Len())
	}
	err = l.Each(func(language.Tuple) error { return nil })
	if err == nil || !strings.Contains(err.Error(), f.Path()) {
		t.Errorf("Each err = %v, want one naming %s", err, f.Path())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// A payload that cannot be written is an output failure, not a finished run.
func TestDriftOutputFailureKeepsTheFileAndHints(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, member("u1"))
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
	state := filepath.Join(t.TempDir(), "w.db")
	c := fs.cli()
	c.JSON = true
	root := New(c).Command()
	var errb bytes.Buffer
	root.SetOut(failingWriter{})
	root.SetErr(&errb)
	root.SetArgs(append([]string{"drift"}, stateArgs(t, driftMapping, state, "--force")...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("want the write error")
	}
	if !exists(state) {
		t.Fatal("the work file was deleted")
	}
	if hint := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(hint, keptLine(state)) {
		t.Errorf("hint = %q", hint)
	}
}
