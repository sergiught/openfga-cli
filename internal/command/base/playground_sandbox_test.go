package base

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

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
		require.Equal(t, want, askKeep(strings.NewReader(in), &out), "input %q", in)
		require.Contains(t, out.String(), "Keep your playground changes? [Y/n]")
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
	require.NoError(t, finishSandbox(f, strings.NewReader("y\n"), &out, nil))
	require.Empty(t, out.String())
	require.Zero(t, f.saved)
	require.Equal(t, 1, f.closed)
}

func TestPlaygroundQuit_NoKeepsOldSave(t *testing.T) {
	configtest.Isolate(t)
	path := filepath.Join(t.TempDir(), "playground.db")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	for _, in := range []string{"n\n", ""} {
		f := &fakeSession{dirty: true, path: path}
		require.NoError(t, finishSandbox(f, strings.NewReader(in), &bytes.Buffer{}, nil))
		require.Zero(t, f.saved)
		require.Equal(t, 1, f.closed)
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "old", string(b))
	}
}

func TestPlaygroundQuit_YesSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	f := &fakeSession{dirty: true, path: path}
	require.NoError(t, finishSandbox(f, strings.NewReader("\n"), &bytes.Buffer{}, nil))
	require.Equal(t, 1, f.saved)
	require.Equal(t, 1, f.closed)
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
	require.NoError(t, c.launchPlayground(root))
	require.Equal(t, 1, real)
	require.Zero(t, sb)

	pg := c.playgroundCmd()
	pg.SetContext(context.Background())
	require.NoError(t, c.launchPlayground(pg))
	require.Equal(t, 1, real)
	require.Equal(t, 1, sb)
}
