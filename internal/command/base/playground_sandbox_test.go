package base

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/command/playground"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func TestAskKeep(t *testing.T) {
	for in, want := range map[string]bool{
		"\n": true, "": false, "y\n": true, "Y\n": true, " yes \n": true, "YES\n": true,
		"n\n": false, "no\n": false, "maybe\ny\n": true, "maybe\nno\n": false,
		"a\nb\nc\ny\n": false, "maybe\n": false, "y": true,
	} {
		var out bytes.Buffer
		if got := askKeep(strings.NewReader(in), &out); got != want {
			t.Fatalf("askKeep(%q) = %v, want %v", in, got, want)
		}
		if !strings.Contains(out.String(), "Keep your playground changes? [Y/n]") {
			t.Fatalf("%q does not contain %q", out.String(), "Keep your playground changes? [Y/n]")
		}
	}
}

type fakeSession struct {
	dirty         bool
	saved, closed int
	path          string
}

func (f *fakeSession) Dirty() bool { return f.dirty }
func (f *fakeSession) Save() error {
	f.saved++
	return os.WriteFile(f.path, []byte("new"), 0o600)
}
func (f *fakeSession) Close() error { f.closed++; return nil }

func TestPlaygroundQuit_NoPromptWhenClean(t *testing.T) {
	configtest.Isolate(t)
	f := &fakeSession{}
	var out bytes.Buffer
	if err := finishSandbox(f, strings.NewReader("y\n"), &out, nil); err != nil {
		t.Fatal(err)
	}
	if len(out.String()) != 0 {
		t.Fatalf("got %v, want empty", out.String())
	}
	if f.saved != 0 {
		t.Fatalf("got %v, want 0", f.saved)
	}
	if f.closed != 1 {
		t.Fatalf("got %v, want %v", f.closed, 1)
	}
}

func TestPlaygroundQuit_NoKeepsOldSave(t *testing.T) {
	configtest.Isolate(t)
	path := filepath.Join(t.TempDir(), "playground.db")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"n\n", ""} {
		f := &fakeSession{dirty: true, path: path}
		if err := finishSandbox(f, strings.NewReader(in), &bytes.Buffer{}, nil); err != nil {
			t.Fatal(err)
		}
		if f.saved != 0 {
			t.Fatalf("got %v, want 0", f.saved)
		}
		if f.closed != 1 {
			t.Fatalf("got %v, want %v", f.closed, 1)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(b); got != "old" {
			t.Fatalf("got %v, want %v", got, "old")
		}
	}
}

func TestPlaygroundQuit_YesSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	f := &fakeSession{dirty: true, path: path}
	if err := finishSandbox(f, strings.NewReader("\n"), &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	if f.saved != 1 {
		t.Fatalf("got %v, want %v", f.saved, 1)
	}
	if f.closed != 1 {
		t.Fatalf("got %v, want %v", f.closed, 1)
	}
}

func TestRootStillOpensRealProfile(t *testing.T) {
	configtest.Isolate(t)
	var real, sb int
	oldR, oldS := runRealPlayground, runSandboxPlayground
	t.Cleanup(func() { runRealPlayground, runSandboxPlayground = oldR, oldS })
	runRealPlayground = func(context.Context, *cli.CLI) error { real++; return nil }
	runSandboxPlayground = func(context.Context, *cli.CLI, playground.SandboxOptions) error { sb++; return nil }

	c := &Command{cli: &cli.CLI{}}
	root := &cobra.Command{Use: "ofga"}
	root.SetContext(context.Background())
	if err := c.launchPlayground(root); err != nil {
		t.Fatal(err)
	}
	if real != 1 {
		t.Fatalf("got %v, want %v", real, 1)
	}
	if sb != 0 {
		t.Fatalf("got %v, want 0", sb)
	}

	pg := c.playgroundCmd()
	pg.SetContext(context.Background())
	if err := c.launchPlayground(pg); err != nil {
		t.Fatal(err)
	}
	if real != 1 {
		t.Fatalf("got %v, want %v", real, 1)
	}
	if sb != 1 {
		t.Fatalf("got %v, want %v", sb, 1)
	}
}

func TestPlaygroundQuit_TUIErrorStillPromptsAndCloses(t *testing.T) {
	configtest.Isolate(t)
	path := filepath.Join(t.TempDir(), "playground.db")
	f := &fakeSession{dirty: true, path: path}
	runErr := errors.New("tui crashed")
	var out bytes.Buffer
	err := finishSandbox(f, strings.NewReader("y\n"), &out, runErr)
	if !errors.Is(err, runErr) {
		t.Fatalf("error = %v, want %v", err, runErr)
	}
	if len(out.String()) == 0 {
		t.Fatal("dirty session must be prompted even when the TUI failed")
	}
	if f.saved != 1 {
		t.Fatalf("got %v, want %v", f.saved, 1)
	}
	if f.closed != 1 {
		t.Fatalf("got %v, want %v", f.closed, 1)
	}
}

func TestPlaygroundQuit_SaveErrorStillCloses(t *testing.T) {
	configtest.Isolate(t)
	f := &fakeSession{dirty: true, path: filepath.Join(t.TempDir(), "missing", "playground.db")}
	err := finishSandbox(f, strings.NewReader("y\n"), &bytes.Buffer{}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if f.saved != 1 {
		t.Fatalf("got %v, want %v", f.saved, 1)
	}
	if f.closed != 1 {
		t.Fatalf("got %v, want %v", f.closed, 1)
	}
}
