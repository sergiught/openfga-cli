package tuple

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/output"
)

// progressEvery is how often a long read reports progress. A store large enough
// for the wait to be worth narrating is large enough that a line per page would
// itself be the noise.
const progressEvery = 50_000

// tupleSink writes tuples out in whichever format is active, streaming wherever
// the format allows it.
//
// Streaming is what keeps `tuples read` bounded: previously every matching
// tuple was accumulated in a slice before anything was written, so reading a
// multi-million-tuple store meant holding all of it in memory and seeing
// nothing until the last page landed. JSON, YAML and plain rows can all be
// emitted as they arrive.
//
// The styled table is the one format that cannot stream — column widths are not
// known until the last row — so it still buffers. That is acceptable because it
// is the interactive format: a table of a million rows is unreadable regardless
// of how it is produced, and anyone reading a store that size is using --json,
// --plain or --output-file.
type tupleSink struct {
	// exactly one of these is set
	stream output.Streamer
	table  *tableSink

	plain bool
	out   io.Writer
	errW  io.Writer

	file *os.File // non-nil when --output-file was given
	path string
	done bool
}

// tableSink buffers rows for the styled table.
type tableSink struct {
	rows [][]string
}

// newTupleSink picks an output strategy from the active format flags. When
// --output-file is set the tuples go to that file and stdout is left free, so
// progress and the summary stay usable while a long export runs.
func newTupleSink(cmd *cobra.Command, c *cli.CLI, path string) (*tupleSink, error) {
	s := &tupleSink{out: cmd.OutOrStdout(), errW: cmd.ErrOrStderr(), path: path}

	if path != "" {
		// 0600 to match the --failed-file bulk path: these are the store's
		// relationship data, not something to widen by default.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open --output-file %s: %w", path, err)
		}
		s.file = f
		s.out = f
	}

	switch {
	case c.JSON || c.YAML:
		s.stream = output.NewStreamer(s.out, c.YAML)
	case output.Plain:
		s.plain = true
	case path != "":
		// A file with no format flag is still machine-bound; JSON is the only
		// sensible default, and it round-trips back through `tuples write --file`.
		s.stream = output.NewStreamer(s.out, false)
	default:
		s.table = &tableSink{}
	}
	return s, nil
}

func (s *tupleSink) add(t openfga.Tuple) error {
	switch {
	case s.stream != nil:
		return s.stream.Write(t)
	case s.plain:
		return output.PlainRow(s.out, tupleFields(t)...)
	default:
		s.table.rows = append(s.table.rows, tupleFields(t))
		return nil
	}
}

// finish closes the output and prints the human summary. n is the number of
// tuples written, tracked by the caller so the streaming paths do not have to.
func (s *tupleSink) finish(n int) error {
	s.done = true

	if s.stream != nil {
		if err := s.stream.Close(); err != nil {
			return err
		}
	}
	if s.table != nil {
		if n == 0 {
			output.Infof(s.errW, "no tuples found")
		} else {
			if err := output.Table(s.out, tupleHeaders, s.table.rows); err != nil {
				return err
			}
			if err := output.HumanBlankLine(s.out); err != nil {
				return err
			}
			output.Infof(s.errW, "%d tuple(s)", n)
		}
	}
	if err := s.closeFile(); err != nil {
		return err
	}
	if s.path != "" {
		output.Successf(s.errW, "wrote %d tuple(s) to %s", n, s.path)
	}
	return nil
}

// abort releases the output file when the command returns early (a read error,
// a cancelled context). It is a no-op once finish has run.
func (s *tupleSink) abort() {
	if s.done {
		return
	}
	_ = s.closeFile()
}

func (s *tupleSink) closeFile() error {
	if s.file == nil {
		return nil
	}
	f := s.file
	s.file = nil
	if err := f.Close(); err != nil {
		return fmt.Errorf("close --output-file %s: %w", s.path, err)
	}
	return nil
}

var tupleHeaders = []string{"USER", "RELATION", "OBJECT", "CONDITION", "WRITTEN"}

func tupleFields(t openfga.Tuple) []string {
	cond := ""
	if t.Key.Condition != nil {
		cond = output.SanitizeField(t.Key.Condition.Name)
	}
	return []string{
		output.SanitizeField(t.Key.User),
		output.SanitizeField(t.Key.Relation),
		output.SanitizeField(t.Key.Object),
		cond,
		t.Timestamp.Format(time.RFC3339),
	}
}

// progress narrates a long-running read. A full export of a large store can run
// for minutes with nothing to show for it, which is indistinguishable from a
// hang; reporting a running count and rate makes the remaining wait estimable.
type progress struct {
	w     io.Writer
	verb  string
	noun  string
	start time.Time
}

func newProgress(w io.Writer, verb, noun string) *progress {
	return &progress{w: w, verb: verb, noun: noun, start: time.Now()}
}

// tick reports every progressEvery items. n is the running total.
func (p *progress) tick(n int) {
	if n == 0 || n%progressEvery != 0 {
		return
	}
	elapsed := time.Since(p.start)
	rate := ""
	if elapsed > 0 {
		rate = fmt.Sprintf(", %.0f/s", float64(n)/elapsed.Seconds())
	}
	output.Progressf(p.w, "%s %d %ss%s", p.verb, n, p.noun, rate)
}

// validateOutputFile rejects an --output-file that cannot be written before any
// request is issued, so a long export does not run to completion and then fail
// at the last step.
func validateOutputFile(path string) error {
	if path == "" {
		return nil
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--output-file %s is a directory", path))
	}
	return nil
}
