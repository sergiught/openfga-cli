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
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

// notCheckedOut is a rule whose tuples drift cannot vouch for.
type notCheckedOut struct {
	Rule   string   `json:"rule"`
	Reason string   `json:"reason"`
	Shapes []string `json:"shapes"`
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
			"The provider is read once, so the result reflects it as of that moment. Auth0 needs a machine-to-machine application. Above 1,000 users the read uses Auth0's user export job, which needs no extra scope. " +
			"--since 2h (or an RFC 3339 time) reads only the Auth0 users changed since then, at most 1,000; other types are still read whole. " +
			"Shapes written by a rule for a windowed type are then not checked for stale tuples.\n\n" +
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
			return c.runDrift(cmd, targetPath(args), provider, since, prune, dryRun, force)
		},
	}
	addProviderFlags(cmd, &provider)
	cmd.Flags().StringVar(&since, "since", "", "read only what changed since a duration ago (2h) or an RFC 3339 time; users only")
	cmd.Flags().BoolVar(&prune, "prune", false, "also delete stale tuples the provider no longer backs")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the drift without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	return cmd
}

func (c *Command) runDrift(cmd *cobra.Command, path, provider, since string, prune, dryRun, force bool) error {
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
		stored            []language.Tuple
		storeReadAt       time.Time
	)
	var storedTotal int
	// The store is read before the provider, so the snapshot is never older
	// than the store it is compared with. The desired tuples are not known yet,
	// so only the tuples the mapping can write are kept.
	run, err := c.runSnapshot(cmd, path, provider, sinceTime, func(f *mapping.File) error {
		var r config.Resolved
		var err error
		if cl, r, err = c.cli.ClientWithStore(); err != nil {
			return err
		}
		storeID = r.StoreID
		if storeNm, storeLbl, err = storeName(ctx, cl, storeID); err != nil {
			return err
		}
		output.Progressf(errw, "reading the store")
		if stored, storedTotal, err = (storeClient{cl}).ReadKept(ctx, snapshot.Writes(f.Doc)); err != nil {
			return err
		}
		storeReadAt = now()
		return nil
	})
	if err != nil {
		return err
	}

	owned, notChecked := snapshot.Coverage(run.File.Doc, run.Types)
	diff := snapshot.Compare(run.Desired, stored, owned)
	label := run.Provider.Label()
	res := driftResult{
		snapshotMeta: newSnapshotMeta(storeID, run, dryRun), StoreReadAt: storeReadAt.UTC(),
		Missing: tuplesOut(diff.Missing), Changed: tuplesOut(diff.Changed), Stale: tuplesOut(diff.Stale),
		NotChecked: make([]notCheckedOut, 0, len(notChecked)),
	}
	for _, n := range notChecked {
		shapes := make([]string, 0, len(n.Shapes))
		for _, s := range n.Shapes {
			shapes = append(shapes, s.String())
		}
		res.NotChecked = append(res.NotChecked, notCheckedOut{Rule: n.Rule, Reason: n.Reason, Shapes: shapes})
	}
	if !machine {
		if err := renderDrift(out, run, storeLbl, storeReadAt, storedTotal, diff, notChecked); err != nil {
			return err
		}
	}
	// finish prints the machine payload last, so it carries what was sent.
	finish := func(err error) error {
		if eerr := emitDrift(out, c.cli, res); eerr != nil && err == nil {
			return eerr
		}
		return err
	}

	if diff.Empty() {
		verb := "match"
		if len(run.Desired) == 1 {
			verb = "matches"
		}
		output.Successf(errw, "no drift: %s %s %s", mapping.Plural(len(run.Desired), "tuple"), verb, output.SanitizeField(label))
		return finish(nil)
	}
	missing, changed, stale := len(diff.Missing), len(diff.Changed), len(diff.Stale)
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
		return finish(remains())
	}
	// --no-input never asks, like a missing terminal: report only.
	if !force && (machine || c.cli.NoInput || !stdinIsTerminal(cmd)) {
		output.Notef(errw, "report only: pass --force to write")
		return finish(remains())
	}

	// send runs one Write and folds what it sent into the document.
	// pruning marks the stale deletes, which res.Pruned counts even when the
	// request fails part-way. Progress counts from prior out of grand changes
	// so a run of several steps reads as one.
	send := func(deletes, writes []language.Tuple, pruning bool, prior, grand int) (snapshot.Sent, error) {
		total := len(deletes) + len(writes)
		sent, err := snapshot.Write(ctx, storeClient{cl}, deletes, writes, func(done, _ int) {
			if grand > backfillRequestSize { // more than one request
				output.Progressf(errw, "sent %d/%d changes", prior+done, grand)
			}
		})
		res.Sent.Writes += sent.Writes
		res.Sent.Deletes += sent.Deletes
		if pruning {
			res.Pruned = sent.Deletes
		}
		if err != nil {
			text := fmt.Sprintf("sent %d of %s before the failure; run the command again to send the rest", sent.Writes+sent.Deletes, mapping.Plural(total, "change"))
			return sent, finish(reportSnapshotFailure(ctx, errw, machine, text, snapshot.Sent(res.Sent), applyHints(err), err))
		}
		return sent, nil
	}
	// replace swaps the changed tuples batch by batch and says what state the
	// store is left in when a batch fails.
	replace := func(prior, grand int) error {
		sent, lost, err := snapshot.Replace(ctx, storeClient{cl}, diff.Was, diff.Changed, func(done, _ int) {
			if grand > backfillRequestSize {
				output.Progressf(errw, "sent %d/%d changes", prior+done, grand)
			}
		})
		res.Sent.Writes += sent.Writes
		res.Sent.Deletes += sent.Deletes
		if err == nil {
			return nil
		}
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
			grand := missing + 2*changed
			var sent snapshot.Sent
			if missing > 0 {
				s, err := send(nil, diff.Missing, false, 0, grand)
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
			_, finishedAt := snapshotTimes(run.Result)
			replaced := ""
			if changed > 0 {
				replaced = ", replacing " + mapping.Plural(changed, "changed tuple")
			}
			output.Successf(errw, "sent %s to store %s%s (%s state as of %s UTC)", mapping.Plural(sent.Writes, "write"),
				output.SanitizeField(storeLbl), replaced, output.SanitizeField(label), finishedAt)
		case !errors.Is(err, prompt.ErrAborted):
			return finish(err)
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
			if err := renderShapes(errw, diff.Stale); err != nil {
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
			sent, err := send(diff.Stale, nil, true, 0, stale)
			if err != nil {
				return err
			}
			pruned = true
			output.Successf(errw, "deleted %s from store %s", mapping.Plural(sent.Deletes, "stale tuple"), output.SanitizeField(storeLbl))
		case !errors.Is(err, prompt.ErrAborted):
			return finish(err)
		}
	}
	if writesDone && (stale == 0 || pruned) {
		return finish(nil)
	}
	return finish(remains())
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
func emitDrift(w io.Writer, c *cli.CLI, res driftResult) error {
	switch {
	case c.JSON || c.YAML:
		return output.Emit(w, c.YAML, res)
	case output.Plain:
		return output.KeyValues(w, [][2]string{
			{"store", res.Store},
			{"provider", res.Provider},
			{"dry_run", strconv.FormatBool(res.DryRun)},
			{"missing", strconv.Itoa(len(res.Missing))},
			{"changed", strconv.Itoa(len(res.Changed))},
			{"stale", strconv.Itoa(len(res.Stale))},
			{"writes", strconv.Itoa(res.Sent.Writes)},
			{"deletes", strconv.Itoa(res.Sent.Deletes)},
		})
	}
	return nil
}
