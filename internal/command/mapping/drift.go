package mapping

import (
	"errors"
	"fmt"
	"io"
	"slices"
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
			"It then asks before writing the missing and changed tuples (a changed tuple is deleted and written again), " +
			"and with --prune asks again before deleting the stale ones. --force skips the questions, --dry-run stops after the report. " +
			"Without a terminal, under --no-input or -o json/yaml, it only reports unless --force is given. " +
			"The provider is read once, so the result reflects it as of that moment. Auth0 needs a machine-to-machine application and reads at most 1,000 users.\n\n" +
			"Exit codes: 0 no drift, or all of it fixed · 1 error · 2 usage · 3 drift remains · 4 network · 130 interrupted.",
		Example: `# See what differs between the store and Auth0
ofga mappings drift auth0.yaml --provider auth0 --dry-run

# Fix it, including stale tuples, without the prompts
ofga mappings drift auth0.yaml --provider auth0 --auth0-client-secret-file ./secret --prune --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runDrift(cmd, targetPath(args), provider, prune, dryRun, force)
		},
	}
	addProviderFlags(cmd, &provider)
	cmd.Flags().BoolVar(&prune, "prune", false, "also delete stale tuples the provider no longer backs")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the drift without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	return cmd
}

func (c *Command) runDrift(cmd *cobra.Command, path, provider string, prune, dryRun, force bool) error {
	machine := c.cli.JSON || c.cli.YAML
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	var (
		cl                *openfga.Client
		storeID           string
		storeNm, storeLbl string
		stored            []language.Tuple
		storeReadAt       time.Time
	)
	// The store is read before the provider, so the snapshot is never older
	// than the store it is compared with.
	run, err := c.runSnapshot(cmd, path, provider, func() error {
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
		if stored, err = (storeClient{cl}).ReadAll(ctx); err != nil {
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
		snapshotMeta: newSnapshotMeta(storeID, run, dryRun), StoreReadAt: storeReadAt,
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
		if err := renderDrift(out, run, storeLbl, storeReadAt, len(stored), diff, notChecked); err != nil {
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
	// request fails part-way.
	send := func(deletes, writes []language.Tuple, pruning bool) (snapshot.Sent, error) {
		total := len(deletes) + len(writes)
		sent, err := snapshot.Write(ctx, storeClient{cl}, deletes, writes, func(done, total int) {
			if total > backfillRequestSize { // more than one request
				output.Progressf(errw, "sent %d/%d changes", done, total)
			}
		})
		res.Sent.Writes += sent.Writes
		res.Sent.Deletes += sent.Deletes
		if pruning {
			res.Pruned = sent.Deletes
		}
		if err != nil {
			text := fmt.Sprintf("sent %d of %s before the failure; run the command again to send the rest", sent.Writes+sent.Deletes, mapping.Plural(total, "change"))
			return sent, finish(reportSnapshotFailure(ctx, errw, machine, text, sent, applyHints(err), err))
		}
		return sent, nil
	}

	if !writesDone {
		if !force {
			output.Notef(errw, "%s", snapshotAge(run.Result))
		}
		switch err := prompt.Confirm(cmd, backfillQuestion(missing+changed, storeNm, storeID), force); {
		case err == nil:
			// A changed tuple is deleted first: a write cannot replace a condition.
			sent, err := send(diff.Changed, slices.Concat(diff.Missing, diff.Changed), false)
			if err != nil {
				return err
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
		switch err := prompt.Confirm(cmd, question, force); {
		case err == nil:
			sent, err := send(diff.Stale, nil, true)
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
