package base

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/command/playground"
	"github.com/sergiught/openfga-cli/internal/sandbox"
)

// Seams so tests can tell which TUI a command launches without starting one.
var (
	runRealPlayground    = playground.Run
	runSandboxPlayground = playground.RunSandbox
)

// launchPlayground opens the sandbox for `ofga playground` and the real-profile
// TUI for bare `ofga`.
func (c *Command) launchPlayground(cmd *cobra.Command) error {
	if cmd.Name() != "playground" {
		return runRealPlayground(cmd.Context(), c.cli)
	}
	reset, _ := cmd.Flags().GetBool("reset")
	return c.runSandbox(cmd.Context(), reset)
}

func (c *Command) runSandbox(ctx context.Context, reset bool) error {
	path, err := sandbox.DefaultPath()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stderr, "Starting playground…")
	sess, err := sandbox.Start(ctx, sandbox.Options{SavedPath: path, Reset: reset})
	if err != nil {
		return err
	}
	// No store or model is preselected: the sandbox holds several stores.
	cl, err := openfga.NewClient(sess.Endpoint())
	if err != nil {
		_ = sess.Close()
		return err
	}
	runErr := runSandboxPlayground(ctx, c.cli, playground.SandboxOptions{
		Client:     cl,
		Endpoint:   sess.Endpoint(),
		Workspaces: sess.Workspaces(),
	})
	return finishSandbox(sess, os.Stdin, os.Stderr, runErr)
}

// sandboxSession is the part of sandbox.Session the quit step needs.
type sandboxSession interface {
	Dirty() bool
	Save() error
	Close() error
}

// finishSandbox runs after the TUI returns: prompt when the session changed,
// save or discard, and always Close. It returns runErr joined with any
// save/close failure.
func finishSandbox(s sandboxSession, in io.Reader, out io.Writer, runErr error) error {
	var saveErr error
	if s.Dirty() {
		// Ctrl+C at the prompt means "no"; without this the default SIGINT
		// handling would kill the process before Close removes the scratch copy.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		keep := askKeepCtx(ctx, in, out)
		stop()
		if keep {
			saveErr = s.Save()
		}
	}
	return errors.Join(runErr, saveErr, s.Close())
}

// askKeepCtx is askKeep that gives up (answering no) when ctx is cancelled.
func askKeepCtx(ctx context.Context, in io.Reader, out io.Writer) bool {
	res := make(chan bool, 1)
	go func() { res <- askKeep(in, out) }()
	select {
	case v := <-res:
		return v
	case <-ctx.Done():
		_, _ = fmt.Fprintln(out)
		return false
	}
}

// askKeep asks whether to keep the playground changes. Enter, y and yes mean
// yes; n, no and EOF mean no. An unrecognised answer is asked again, so a typo
// never discards work; after three in a row it counts as no (the previous save
// stays untouched).
func askKeep(in io.Reader, out io.Writer) bool {
	r := bufio.NewReader(in)
	for range 3 {
		_, _ = fmt.Fprint(out, "Keep your playground changes? [Y/n] ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			_, _ = fmt.Fprintln(out)
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			return true
		case "n", "no":
			return false
		}
		_, _ = fmt.Fprintln(out, "Please answer y or n.")
	}
	return false
}
