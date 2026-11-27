package mapping

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/mapping/status"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

// notCheckedOut is a rule whose tuples drift cannot vouch for.
type notCheckedOut struct {
	Rule   string   `json:"rule"`
	Reason string   `json:"reason"`
	Shapes []string `json:"shapes"`
	// Tuples are the contested tuples a rule's entry stands for.
	Tuples []tupleOut `json:"tuples,omitempty"`
}

// notCheckedOuts is the JSON form of the not-checked entries, never nil.
func notCheckedOuts(ns []snapshot.NotChecked) []notCheckedOut {
	out := make([]notCheckedOut, 0, len(ns))
	for _, n := range ns {
		shapes := make([]string, 0, len(n.Shapes))
		for _, s := range n.Shapes {
			shapes = append(shapes, s.String())
		}
		o := notCheckedOut{Rule: n.Rule, Reason: n.Reason, Shapes: shapes}
		if len(n.Tuples) > 0 {
			o.Tuples = tuplesOut(n.Tuples)
		}
		out = append(out, o)
	}
	return out
}

// driftResult is the document `mappings drift` prints under -o json/yaml.
type driftResult struct {
	snapshotMeta
	StoreReadAt time.Time       `json:"store_read_at"`
	Missing     []tupleOut      `json:"missing"`
	Changed     []tupleOut      `json:"changed"`
	Stale       []tupleOut      `json:"stale"`
	NotChecked  []notCheckedOut `json:"not_checked"`
	Sent        sentOut         `json:"sent"`
	Pruned      int             `json:"pruned"`
}

func (c *Command) newDriftCmd() *cobra.Command {
	var (
		provider             string
		since                string
		prune, dryRun, force bool
		state                stateFlags
	)
	cmd := &cobra.Command{
		Use:   "drift [file] --provider <name>",
		Short: "Compare the store with an identity provider's current state and fix the difference",
		Long: "Read the active profile's store, read an identity provider's current state, replay it through a mapping file, and compare the two. With no file argument it uses ./mapping.yaml.\n\n" +
			"The report lists tuples the provider backs that the store lacks (missing), tuples the store holds under another condition (changed), " +
			"and tuples the store holds that the provider no longer backs (stale). Only shapes a snapshot rule writes are checked for stale tuples; " +
			"rules that write anything else are listed as not checked and never change the exit code. " +
			"A rule takes part when its `when` also names a snapshot type, such as `input.type == \"snapshot.organization.member\"`.\n\n" +
			"It then asks before writing the missing tuples and replacing the changed ones (a changed tuple is deleted and written again, 100 at a time, and put back if that fails), " +
			"and with --prune asks again before deleting the stale ones. --force skips the questions, --dry-run stops after the report. " +
			"Without a terminal, under --no-input or -o json/yaml, it only reports unless --force is given. " +
			"The provider is read once, so the result reflects it as of that moment. Auth0 needs a machine-to-machine application. From 1,000 users up the read uses Auth0's user export job, which needs no extra scope. " +
			"--since 2h (or an RFC 3339 time) reads only the Auth0 users changed since then, at most 1,000; other types are still read whole. " +
			"Shapes written by a rule that can fire on a windowed type are then not checked for stale tuples.\n\n" +
			"Exit codes: 0 no drift, or all of it fixed · 1 error · 2 usage · 3 drift remains · 4 network · 130 interrupted.",
		Example: `# See what differs between the store and Auth0
ofga mappings drift auth0.yaml --provider auth0 --dry-run

# Check only the users changed in the last two hours
ofga mappings drift auth0.yaml --provider auth0 --since 2h --dry-run

# Fix it, including stale tuples, without the prompts
ofga mappings drift auth0.yaml --provider auth0 --auth0-client-secret-file ./secret --prune --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runDrift(cmd, targetPath(args), provider, since, prune, dryRun, force, &state)
		},
	}
	addProviderFlags(cmd, &provider)
	cmd.Flags().StringVar(&since, "since", "", "read only what changed since a duration ago (2h) or an RFC 3339 time; users only")
	cmd.Flags().BoolVar(&prune, "prune", false, "also delete stale tuples the provider no longer backs")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the drift without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	state.register(cmd.Flags())
	return cmd
}

// runDrift runs drift and then settles the work file by how it ended.
func (c *Command) runDrift(cmd *cobra.Command, path, provider, since string, prune, dryRun, force bool, state *stateFlags) error {
	ws := &workState{}
	err := c.drift(cmd, path, provider, since, prune, dryRun, force, state, ws)
	return ws.end(c, cmd, state, err, force, c.cli.JSON || c.cli.YAML)
}

func (c *Command) drift(cmd *cobra.Command, path, provider, since string, prune, dryRun, force bool, state *stateFlags, ws *workState) error {
	sinceTime, err := parseSince(since)
	if err != nil {
		return err
	}
	machine := c.cli.JSON || c.cli.YAML
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	var (
		cl                *openfga.Client
		storeID           string
		storeNm, storeLbl string
		storeReadAt       time.Time
	)
	var (
		storedTotal int
		// usersOnly is a --since run in which every shape is windowed, so
		// nothing can be stale and the store is read for the desired tuples only.
		usersOnly bool
		read      storeRead
		afterOpen func(wf *workfile.File) error
	)
	// The store is read before the provider, so the snapshot is never older
	// than the store it is compared with. The desired tuples are not known yet,
	// so only the tuples the mapping can write are kept. With nothing left to
	// check for stale, the store is read after the provider instead.
	meter := new(client.Meter)
	var live *status.Live
	defer func() { live.Close() }()
	run, err := c.runSnapshot(cmd, path, provider, sinceTime, snapshotOpts{command: "drift", sinceText: since, state: state, ws: ws, storeFirst: func() bool { return !usersOnly }}, func() *status.Live { return live }, func(f *mapping.File, types []snapshot.Type, windowed []string) (string, error) {
		var r config.Resolved
		var err error
		if cl, r, err = c.cli.ClientWithStore(client.WithMeter(meter)); err != nil {
			return "", err
		}
		storeID = r.StoreID
		if storeNm, storeLbl, err = storeName(ctx, cl, storeID); err != nil {
			return "", err
		}
		if !sinceTime.IsZero() {
			owned, _ := snapshot.Coverage(f.Doc, types, windowed, sinceTime)
			usersOnly = len(owned) == 0
		}
		// The phases are named in the order they will run. A users-only run
		// has nothing stale, so Prune never runs.
		label := providers[provider].Label
		phases := []string{"Store", label}
		if usersOnly {
			phases = []string{label, "Store"}
		}
		if !dryRun {
			phases = append(phases, "Write")
			if prune && !usersOnly {
				phases = append(phases, "Prune")
			}
		}
		live = c.liveStatus(errw, machine, "ofga mappings drift", phases...)
		if !usersOnly {
			doc := f.Doc
			afterOpen = func(wf *workfile.File) error {
				live.Phase("Store")
				live.Store(meter)
				if live == nil {
					output.Progressf(errw, "reading the store")
				}
				var kept int
				var err error
				_, storedTotal, err = (storeClient{cl}).ReadKept(ctx, snapshot.Writes(doc), func(ts []language.Tuple) error {
					kept += len(ts)
					return workErr(wf.Path(), wf.AddStore(ctx, ts))
				}, func(read, _ int) {
					live.Set("tuples", read, 0)
				})
				if err != nil {
					return err
				}
				if err := wf.MarkStoreRead(now(), storedTotal, 0); err != nil {
					return workErr(wf.Path(), err)
				}
				if err := wf.Remember(storeBeforeProvider, "1"); err != nil {
					return workErr(wf.Path(), err)
				}
				live.Note(thousands(storedTotal) + " tuples read · " + thousands(kept) + " kept")
				storeReadAt = now()
				return nil
			}
		}
		return storeID, nil
	}, func(wf *workfile.File) error {
		if afterOpen == nil {
			return nil
		}
		return afterOpen(wf)
	})
	if err != nil {
		return err
	}
	wf := run.Work
	workPath := wf.Path()
	nDesired := count(run.Desired)
	if run.StoreSaved {
		// The store read of the run being continued is compared with the saved
		// provider data; a newer one could undo a change made since.
		storeReadAt, storedTotal, _, _ = wf.StoreRead()
	}

	if usersOnly && nDesired > 0 {
		live.Phase("Store")
		live.Store(meter)
		if live == nil {
			output.Progressf(errw, "reading the store")
		}
		total, objects, err := (storeClient{cl}).ReadMatchingFrom(ctx, wf.Wanted(), func(ts []language.Tuple) error {
			return workErr(workPath, wf.AddStore(ctx, ts))
		}, func(mp matchProgress) {
			switch {
			case live != nil:
				matchStatus(live, mp)
			case mp.Step == "objects" && mp.Done == 0:
				output.Progressf(errw, "reading %s in the store", mapping.Plural(mp.Objects, "object"))
			}
		})
		if err != nil {
			return err
		}
		// total counts every read, crowded pairs included; it is not a row count.
		read = storeRead{Read: total, Objects: objects}
		if err := wf.MarkStoreRead(now(), total, objects); err != nil {
			return workErr(workPath, err)
		}
		live.Note(matchNote(read))
		storedTotal, storeReadAt = total, now()
	}
	// Everything the reads have to say comes next, so the card gives way.
	live.Suspend("read")

	owned, notChecked := snapshot.Coverage(run.File.Doc, run.Types, run.Windowed, run.Since)
	notChecked = append(notChecked, snapshot.Removers(run.File.Doc, run.Contested)...)
	// A contested tuple is neither stale nor missing, so Compare leaves it out.
	counts, err := wf.Compare(ctx, owned, run.Contested)
	if err != nil {
		return workErr(workPath, err)
	}
	label := run.Provider.Label()
	res := driftResult{
		snapshotMeta: newSnapshotMeta(storeID, run, dryRun), StoreReadAt: storeReadAt.UTC(),
		NotChecked: notCheckedOuts(notChecked),
	}
	earlier := run.sent()
	res.Sent = sentOut{Writes: earlier.Writes, Deletes: earlier.Deletes}
	res.Pruned = run.Earlier[workfile.Stale]
	desired, dl := run.Desired, diffLists(wf, counts)
	lists := map[string]snapshot.Tuples{"missing": dl.Missing, "changed": dl.Changed, "stale": dl.Stale}
	if !machine {
		if err := renderDrift(out, run, storeLbl, storeReadAt, storedTotal, read.Objects, usersOnly, desired, dl, notChecked); err != nil {
			return err
		}
	}
	// finish prints the machine payload last, so it carries what was sent.
	emitFailed := false
	finish := func(err error) error {
		if eerr := emitDrift(out, c.cli, res, lists); eerr != nil {
			emitFailed = true
			if err == nil {
				return eerr
			}
		}
		return err
	}

	// done is finish for the ends that are not failures: the work file is then
	// settled by the keep step instead of kept for a resume.
	// A failed payload is an output failure: the run is not finished then, so
	// the file is kept with its hint.
	done := func(err error) error {
		err = finish(err)
		ws.finished = !emitFailed
		return err
	}
	if counts.Missing+counts.Changed+counts.Stale == 0 {
		verb := "match"
		if nDesired == 1 {
			verb = "matches"
		}
		if run.Since.IsZero() {
			output.Successf(errw, "no drift: %s %s %s", bold(mapping.Plural(nDesired, "tuple")), verb, output.SanitizeField(label))
		} else {
			output.Successf(errw, "no drift found: %s %s %s; --since read only the users changed since %s UTC, so tuples of other users were not checked",
				bold(mapping.Plural(nDesired, "tuple")), verb, output.SanitizeField(label), run.Since.UTC().Format(dateClockLayout))
		}
		return done(nil)
	}
	missing, changed, stale := counts.Missing, counts.Changed, counts.Stale
	writesDone, pruned := missing+changed == 0, false
	remains := func() error {
		m, ch, s := missing, changed, stale
		if writesDone {
			m, ch = 0, 0
		}
		if pruned {
			s = 0
		}
		return clierr.Silent(clierr.CodeTestFailed, fmt.Sprintf("drift remains: %d missing, %d changed, %d stale", m, ch, s))
	}
	if dryRun {
		if !machine {
			output.Notef(errw, "dry run: nothing written")
		}
		return done(remains())
	}
	// --no-input never asks, like a missing terminal: report only.
	if !force && (machine || c.cli.NoInput || !stdinIsTerminal(cmd)) {
		switch {
		case missing+changed > 0 || stale == 0:
			output.Notef(errw, "report only: pass --force to write")
		case prune:
			output.Notef(errw, "report only: pass --force to delete %s", mapping.Plural(stale, "stale tuple"))
		default:
			output.Notef(errw, "report only: pass --prune --force to delete %s", mapping.Plural(stale, "stale tuple"))
		}
		return done(remains())
	}

	// send runs one Write and folds what it sent into the document.
	// pruning marks the stale deletes, which res.Pruned counts even when the
	// request fails part-way. Progress counts from prior out of grand changes
	// so a run of several steps reads as one.
	send := func(pruning bool, total, prior, grand int) (snapshot.Sent, error) {
		verb, kind := "wrote", workfile.Missing
		if pruning {
			verb, kind = "pruned", workfile.Stale
		}
		sent, err := sendPages(wf, kind, func(page, _ []language.Tuple, before int) (snapshot.Sent, error) {
			progress := func(done, _ int) {
				switch {
				case live != nil:
					live.Set("changes", prior+before+done, grand)
				case grand > backfillRequestSize: // more than one request
					output.Progressf(errw, "sent %d/%d changes", prior+before+done, grand)
				}
			}
			if pruning {
				return snapshot.Write(ctx, storeClient{cl}, page, nil, progress)
			}
			return snapshot.Write(ctx, storeClient{cl}, nil, page, progress)
		})
		err = wf.Named(err)
		res.Sent.Writes += sent.Writes
		res.Sent.Deletes += sent.Deletes
		if pruning {
			res.Pruned = sent.Deletes + run.Earlier[workfile.Stale]
		}
		if err != nil {
			live.Note(thousands(sent.Writes+sent.Deletes) + " changes")
			live.Suspend(verb)
			again := "run the command again"
			if pruning {
				again += " with --prune"
			}
			text := fmt.Sprintf("sent %d of %s before the failure; %s to send the rest", sent.Writes+sent.Deletes, mapping.Plural(total, "change"), again)
			return sent, finish(reportSnapshotFailure(ctx, errw, machine, text, snapshot.Sent(res.Sent), applyHints(err), err))
		}
		return sent, nil
	}
	// replace swaps the changed tuples batch by batch and says what state the
	// store is left in when a batch fails.
	replace := func(prior, grand int) error {
		var lost []language.Tuple
		sent, err := sendPages(wf, workfile.Changed, func(page, was []language.Tuple, before int) (snapshot.Sent, error) {
			s, l, err := snapshot.Replace(ctx, storeClient{cl}, was, page, func(done, _ int) {
				switch {
				case live != nil:
					live.Set("changes", prior+2*before+done, grand)
				case grand > backfillRequestSize:
					output.Progressf(errw, "sent %d/%d changes", prior+2*before+done, grand)
				}
			})
			lost = l
			return s, err
		})
		err = wf.Named(err)
		res.Sent.Writes += sent.Writes
		res.Sent.Deletes += sent.Deletes
		if err == nil {
			return nil
		}
		live.Note(thousands(sent.Writes+sent.Deletes) + " changes")
		live.Suspend("wrote")
		hints := applyHints(err)
		var text string
		switch {
		case len(lost) > 0 && sent.Deletes > sent.Writes:
			text = fmt.Sprintf("deleted %s and could not write the stored version back", mapping.Plural(len(lost), "tuple"))
			hints = append(hints, lostHint)
		case len(lost) > 0:
			text = fmt.Sprintf("deleting %s failed and so did writing the stored version back; the store may have lost what is listed below", mapping.Plural(len(lost), "tuple"))
			hints = append(hints, lostHint)
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			text = fmt.Sprintf("replaced %d of %s before the interruption; run the command again to replace the rest", sent.Writes, mapping.Plural(changed, "changed tuple"))
		case sent.Writes > 0:
			text = fmt.Sprintf("replaced %d of %s; the next batch failed and its stored versions are in place", sent.Writes, mapping.Plural(changed, "changed tuple"))
		default:
			text = fmt.Sprintf("could not replace %s; the stored versions are in place, nothing was replaced", mapping.Plural(changed, "changed tuple"))
		}
		if prior > 0 {
			text = fmt.Sprintf("wrote %s, then %s", mapping.Plural(prior, "missing tuple"), text)
		}
		err = reportSnapshotFailure(ctx, errw, machine, text, snapshot.Sent(res.Sent), hints, err)
		printLost(errw, lost)
		return finish(err)
	}

	if !writesDone {
		if !force {
			output.Notef(errw, "%s", snapshotAge(run.Result))
		}
		err := prompt.Confirm(cmd, driftQuestion(missing, changed, storeNm, storeID), force)
		if ctx.Err() != nil {
			return finish(ctx.Err())
		}
		switch {
		case err == nil:
			live.Phase("Write")
			grand := missing + 2*changed
			var sent snapshot.Sent
			if missing > 0 {
				s, err := send(false, missing, 0, grand)
				if err != nil {
					return err
				}
				sent = s
			}
			if changed > 0 {
				if err := replace(missing, grand); err != nil {
					return err
				}
				sent.Writes += changed
			}
			writesDone = true
			live.Note(thousands(res.Sent.Writes+res.Sent.Deletes) + " changes")
			live.Suspend("wrote")
			_, finishedAt := snapshotTimes(run.Result)
			replaced := ""
			if changed > 0 {
				replaced = ", replacing " + mapping.Plural(changed, "changed tuple")
			}
			output.Successf(errw, "sent %s to store %s%s (%s state as of %s UTC)", bold(mapping.Plural(sent.Writes, "write")),
				output.SanitizeField(storeLbl), replaced, output.SanitizeField(label), finishedAt)
		case !errors.Is(err, prompt.ErrAborted):
			return done(err)
		}
	}

	switch {
	case stale == 0:
	case !prune:
		it := "them"
		if stale == 1 {
			it = "it"
		}
		output.Notef(errw, "%s left alone; run again with --prune to delete %s", mapping.Plural(stale, "stale tuple"), it)
	default:
		if !force {
			shapes := renderShapes
			if !output.Plain {
				shapes = renderShapeLines
			}
			if err := shapes(errw, dl.Stale); err != nil {
				return err
			}
			output.Notef(errw, "%s", snapshotAge(run.Result))
		}
		question := fmt.Sprintf("Delete %s from store %s?", mapping.Plural(stale, "stale tuple"), storeTarget(storeNm, storeID))
		err := prompt.Confirm(cmd, question, force)
		if ctx.Err() != nil {
			return finish(ctx.Err())
		}
		switch {
		case err == nil:
			live.Phase("Prune")
			sent, err := send(true, stale, 0, stale)
			if err != nil {
				return err
			}
			pruned = true
			live.Note(thousands(sent.Deletes) + " changes")
			live.Suspend("pruned")
			output.Successf(errw, "deleted %s from store %s", bold(mapping.Plural(sent.Deletes, "stale tuple")), output.SanitizeField(storeLbl))
		case !errors.Is(err, prompt.ErrAborted):
			return done(err)
		}
	}
	if writesDone && (stale == 0 || pruned) {
		return done(nil)
	}
	return done(remains())
}

// driftQuestion is the write prompt; it names the delete a changed tuple costs.
func driftQuestion(missing, changed int, name, id string) string {
	switch {
	case changed == 0:
		return backfillQuestion(missing, name, id)
	case missing == 0:
		return fmt.Sprintf("Replace %s (delete, then write) in store %s?", mapping.Plural(changed, "changed tuple"), storeTarget(name, id))
	}
	return fmt.Sprintf("Write %s and replace %s (delete, then write) in store %s?",
		mapping.Plural(missing, "tuple"), mapping.Plural(changed, "changed tuple"), storeTarget(name, id))
}

// lostHint follows a replace that could not put the stored versions back.
const lostHint = "run the command again once the cause is fixed: it reports them as missing or changed and writes the desired version"

// printLost lists tuples a failed replace may have left deleted, at most
// maxPlanLines.
func printLost(w io.Writer, lost []language.Tuple) {
	field := output.SanitizeField
	if output.Plain {
		field = output.PlainField
	}
	for _, t := range lost[:min(len(lost), maxPlanLines)] {
		t.Action, t.Context = language.ActionDelete, nil
		_, _ = fmt.Fprintln(w, mapping.ChangeLine(t, field))
	}
	if more := len(lost) - maxPlanLines; more > 0 {
		_, _ = fmt.Fprintf(w, "… %d more\n", more)
	}
}

// emitDrift prints the machine payload for -o json/yaml, or key/values in
// plain mode. Human mode has already printed everything.
//
// The result's list fields are left empty: lists holds them by JSON name
// ("missing", "changed", "stale"). -o json streams them; the other formats
// read them into the result first.
func emitDrift(w io.Writer, c *cli.CLI, res driftResult, lists map[string]snapshot.Tuples) error {
	switch {
	case streamsJSON(c):
		return streamJSON(w, res, lists)
	case c.JSON || c.YAML:
		var err error
		if res.Missing, err = materialize(lists["missing"]); err != nil {
			return err
		}
		if res.Changed, err = materialize(lists["changed"]); err != nil {
			return err
		}
		if res.Stale, err = materialize(lists["stale"]); err != nil {
			return err
		}
		return output.Emit(w, c.YAML, res)
	case output.Plain:
		return output.KeyValues(w, [][2]string{
			{"store", res.Store},
			{"provider", res.Provider},
			{"dry_run", strconv.FormatBool(res.DryRun)},
			{"missing", strconv.Itoa(count(lists["missing"]))},
			{"changed", strconv.Itoa(count(lists["changed"]))},
			{"stale", strconv.Itoa(count(lists["stale"]))},
			{"not_checked", strconv.Itoa(len(res.NotChecked))},
			{"pruned", strconv.Itoa(res.Pruned)},
			{"writes", strconv.Itoa(res.Sent.Writes)},
			{"deletes", strconv.Itoa(res.Sent.Deletes)},
		})
	}
	return nil
}
