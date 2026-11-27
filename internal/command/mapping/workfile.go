package mapping

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

// cacheDir finds the user cache directory the default work file goes under;
// tests replace it.
var cacheDir = os.UserCacheDir

// stateFlags are the flags that say where the work file lives and what happens
// to it.
type stateFlags struct {
	path         string
	resume, keep bool
}

func (sf *stateFlags) register(fs *pflag.FlagSet) {
	fs.StringVar(&sf.path, "state", "", "work file that keeps what the run reads and plans (default: a file in the user cache directory named after the mapping and store)")
	fs.BoolVar(&sf.resume, "resume", false, "continue from the work file of a run that did not finish")
	fs.BoolVar(&sf.keep, "keep-state", false, "keep the work file after the run finishes, without asking")
}

// workState follows one command's work file from its creation to the end of
// the run.
type workState struct {
	f *workfile.File
	// finished is set on every path that ends the run normally, whatever its exit
	// code: the report was printed, the write was done or declined. Any other
	// end of a run with a file is a failure that keeps it.
	finished bool
}

// keptHint is the line a failed run ends with.
func keptHint(path string) string {
	return "work file kept at " + path + "; run again with --resume to continue"
}

// end closes the work file at the end of a run whose outcome is err. A failure
// keeps the file and says how to continue; a finished run keeps it with
// --keep-state or when asked, and deletes it otherwise.
func (ws *workState) end(c *Command, cmd *cobra.Command, sf *stateFlags, err error, force, machine bool) error {
	f := ws.f
	if f == nil {
		return err
	}
	ws.f = nil
	path := f.Path()
	if !ws.finished {
		_ = f.Close()
		if machine {
			// The error envelope is the only thing on stderr.
			return clierr.AddHint(keptHint(path), err)
		}
		output.Hintf(cmd.ErrOrStderr(), "%s", keptHint(path))
		return err
	}
	keep := sf.keep
	if !keep && !force && !machine && !c.cli.NoInput && stdinIsTerminal(cmd) {
		keep = prompt.Confirm(cmd, "Keep the work file at "+output.SanitizeField(path)+" to reuse?", false) == nil
	}
	if keep {
		if cerr := f.Close(); cerr != nil && err == nil {
			return workErr(path, cerr)
		}
		return err
	}
	if rerr := f.Remove(); rerr != nil {
		if err == nil {
			return workErr(path, rerr)
		}
		output.Warnf(cmd.ErrOrStderr(), "could not delete the work file: %v", workErr(path, rerr))
	}
	return err
}

// workErr names the work file in err unless err already does.
func workErr(path string, err error) error {
	if err == nil || strings.Contains(err.Error(), path) {
		return err
	}
	return fmt.Errorf("work file %s: %w", path, err)
}

// workPath is where this run's work file goes: --state, or a file under the
// cache directory named after the mapping, the store and the command.
func workPath(sf *stateFlags, mappingPath, storeID, command string) (string, error) {
	if sf.path != "" {
		return sf.path, nil
	}
	dir, err := cacheDir()
	if err != nil {
		return "", noCacheDir(err)
	}
	p, err := workfile.DefaultPath(dir, mappingPath, storeID, command)
	if err != nil {
		return "", noCacheDir(err)
	}
	return p, nil
}

func noCacheDir(err error) error {
	return clierr.WithHint("pass --state FILE to choose where the work file goes",
		fmt.Errorf("no place for the work file: %w", err))
}

// createWork makes the run's work file at path, replacing the file an earlier
// run left there and saying so.
func createWork(cmd *cobra.Command, path string, meta workfile.Meta) (*workfile.File, error) {
	if old, err := workfile.Open(path); err == nil {
		at := old.Meta().CreatedAt
		_ = old.Close()
		if at.IsZero() {
			output.Notef(cmd.ErrOrStderr(), "discarding the unfinished run at %s", path)
		} else {
			output.Notef(cmd.ErrOrStderr(), "discarding the unfinished run from %s at %s", at.UTC().Format("2006-01-02 15:04")+" UTC", path)
		}
	}
	f, err := workfile.Create(path, meta)
	if err != nil {
		return nil, workErr(path, err)
	}
	return f, nil
}

// workMeta describes the run for the work file.
func workMeta(command, providerName, tenant, storeID, sinceText string, since time.Time, requested []string, src []byte) workfile.Meta {
	sum := sha256.Sum256(src)
	return workfile.Meta{
		Command: command, Provider: providerName, Tenant: tenant, StoreID: storeID,
		Since: since, SinceText: sinceText, Types: requested,
		MappingSHA256: hex.EncodeToString(sum[:]), CreatedAt: now(),
	}
}

// counted is a list whose length is already known, so a report never asks the
// work file to count and cannot mistake a failed count for an empty list. The
// read itself still reports its errors.
type counted struct {
	snapshot.Tuples
	n int
}

func (c counted) Len() int { return c.n }

// diffLists is the work file's diff as lists that know their lengths.
func diffLists(f *workfile.File, c workfile.Counts) snapshot.DiffLists {
	d := f.Diff()
	return snapshot.DiffLists{
		Missing: counted{d.Missing, c.Missing}, Changed: counted{d.Changed, c.Changed},
		Was: counted{d.Was, c.Changed}, Stale: counted{d.Stale, c.Stale},
	}
}

// resumeRefused is how a run that cannot continue from its work file ends: a
// usage error that says why and what to do instead. The file is left alone.
func resumeRefused(err error) error {
	return clierr.WithCode(clierr.CodeUsage, clierr.WithHint("run without --resume to start over", err))
}

// openResume opens the work file at path for a run described by want, refusing
// a file that is missing or that another run made.
func openResume(path string, want workfile.Meta) (*workfile.File, error) {
	wf, err := workfile.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, resumeRefused(&workfile.RefusalError{Path: path, Reason: "there is no work file"})
		}
		return nil, resumeRefused(err)
	}
	if err := wf.Check(want, now()); err != nil {
		_ = wf.Close()
		var refusal *workfile.RefusalError
		if errors.As(err, &refusal) {
			return nil, resumeRefused(err)
		}
		return nil, workErr(path, err)
	}
	return wf, nil
}

// storeBeforeProvider is the work file's note that its store read was taken
// before the provider was read (a ReadKept read), not after it.
const storeBeforeProvider = "store.before-provider"

// storeWritesKey is the work file's note of the shapes its store read covered.
const storeWritesKey = "store.writes"

// writtenShapes lists the shapes the mapping writes, sorted, or "*" when a rule
// templates its type or relation and so can write any.
func writtenShapes(doc *mapping.Document) []string {
	seen := map[string]bool{}
	for _, r := range doc.Rules {
		shapes, unshaped := mapping.RuleShapes(r)
		if unshaped > 0 {
			seen["*"] = true
		}
		for _, s := range shapes {
			seen[s.String()] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// uncovered reports the first of want that saved does not hold; ok is true when
// saved covers all of want. A saved "*" covers everything.
func uncovered(want, saved []string) (first string, ok bool) {
	if slices.Contains(saved, "*") {
		return "", true
	}
	for _, w := range want {
		if !slices.Contains(saved, w) {
			if w == "*" {
				w = "a templated type or relation"
			}
			return w, false
		}
	}
	return "", true
}

// prepareResume readies an open work file for the run that continues it: units
// that never finished are dropped, so the provider reads them again, and
// everything the replay builds is emptied, so the saved events replay from the
// start. A store read is kept only when the run reads the store before the
// provider (beforeProvider) and the earlier run read it that way too. A run
// that reads the store before the provider cannot continue provider data saved
// next to a store read taken after it, so it is refused; with no unit saved
// yet, or for a run that reads the store after the provider, the store read is
// emptied and read again as in a fresh run. It reports what the earlier runs
// sent (earlier), whether any unit was already read and whether a store read
// was kept. The kept store read mirrors what was sent, so the comparison
// leaves out what the earlier runs already wrote.
func prepareResume(ctx context.Context, wf *workfile.File, beforeProvider bool, writes []string) (earlier map[workfile.DiffKind]int, resumed, storeSaved bool, err error) {
	if err = wf.DropUnfinished(); err != nil {
		return nil, false, false, err
	}
	if started, _ := wf.Window(); !started.IsZero() {
		resumed = true
	}
	_, _, _, readSaved := wf.StoreRead()
	keep := beforeProvider && readSaved && wf.Recall(storeBeforeProvider) == "1"
	if beforeProvider && resumed && !keep {
		return nil, false, false, resumeRefused(&workfile.RefusalError{Path: wf.Path(),
			Reason: "the earlier run read the store after the provider; this run must read it before"})
	}
	if keep {
		if shape, ok := uncovered(writes, strings.Split(wf.Recall(storeWritesKey), "\n")); !ok {
			return nil, false, false, resumeRefused(&workfile.RefusalError{Path: wf.Path(),
				Reason: "the mapping now writes " + shape + " that the earlier store read did not cover"})
		}
	}
	if err = wf.ResetReplay(ctx); err != nil {
		return nil, false, false, err
	}
	if keep {
		storeSaved = true
	} else if err = errors.Join(wf.ResetStore(ctx), wf.ResetSent(), wf.Remember(storeBeforeProvider, "")); err != nil {
		return nil, false, false, err
	}
	earlier = map[workfile.DiffKind]int{}
	for _, kind := range []workfile.DiffKind{workfile.Missing, workfile.Changed, workfile.Stale} {
		n, err := wf.SentCount(kind)
		if err != nil {
			return nil, false, false, err
		}
		earlier[kind] = n + wf.Carried(kind)
	}
	return earlier, resumed, storeSaved, nil
}
