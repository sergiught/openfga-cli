package mapping

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
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

// backfillResult is the document `mappings backfill` prints under -o json/yaml.
type backfillResult struct {
	snapshotMeta
	StoreReadAt time.Time  `json:"store_read_at"`
	Tuples      []tupleOut `json:"tuples"`
	Missing     []tupleOut `json:"missing"`
	Changed     []tupleOut `json:"changed"`
	Existing    int        `json:"existing"`
	Sent        sentOut    `json:"sent"`
}

// sentOut is how many changes reached the store.
type sentOut struct {
	Writes  int `json:"writes"`
	Deletes int `json:"deletes"`
}

func (c *Command) newBackfillCmd() *cobra.Command {
	var (
		provider      string
		dryRun, force bool
	)
	cmd := &cobra.Command{
		Use:   "backfill [file] --provider <name>",
		Short: "Write the tuples an identity provider's current state maps to",
		Long: "Read an identity provider's current state, replay it through a mapping file, and write the resulting tuples to the active profile's store. With no file argument it uses ./mapping.yaml.\n\n" +
			"The read comes back as snapshot events, one per user, organization, membership and so on, which the mapping's rules handle like live events. " +
			"A rule takes part when its `when` also names a snapshot type, such as `input.type == \"snapshot.organization.member\"`; " +
			"a mapping with none has nothing to backfill, and the error suggests the clause for each rule. " +
			"Only the types the mapping names are read.\n\n" +
			"backfill reads the store first and writes only the tuples it lacks. It never deletes, and a tuple that is already there with a different condition is left for drift to replace. " +
			"It shows the tuples it would write and asks before writing (--force skips the question, --dry-run stops after showing them). " +
			"The provider is read once, so the tuples reflect it as of that moment. Changes made while the command runs, or after it, are not included. " +
			"Use `ofga mappings drift` to compare the store with the provider and to remove tuples the provider no longer backs.\n\n" +
			"Run again after a failure: tuples that already landed are not written twice. Auth0 needs a machine-to-machine application. Above 1,000 users Auth0's list stops, so the read asks Auth0 for a user export job, waits for it and downloads it; this needs no extra scope. On a small tenant the job takes about a second, on a large one Auth0 decides. An exported identity may carry a profileData the list does not.\n\n" +
			"Exit codes: 0 ok · 1 error · 2 usage · 4 network · 130 interrupted.",
		Example: `# See what a backfill from Auth0 would write
ofga mappings backfill auth0.yaml --provider auth0 --dry-run

# Write it without the prompt, in a script
ofga mappings backfill auth0.yaml --provider auth0 --auth0-client-secret-file ./secret --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runBackfill(cmd, targetPath(args), provider, dryRun, force)
		},
	}
	addProviderFlags(cmd, &provider)
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the tuples without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	return cmd
}

func (c *Command) runBackfill(cmd *cobra.Command, path, provider string, dryRun, force bool) error {
	machine := c.cli.JSON || c.cli.YAML
	if machine && !dryRun && !force {
		// Machine output covers -o json/yaml/jsonl and agent mode, which has no -o.
		return clierr.WithCode(clierr.CodeUsage, clierr.WithHint("pass --dry-run to preview or --force to write",
			errors.New("non-interactive runs never prompt; pass --dry-run to preview or --force to write")))
	}
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	var (
		cl                *openfga.Client
		storeID           string
		storeNm, storeLbl string
		stored            []language.Tuple
		storeReadAt       time.Time
	)
	// The store is resolved and read before the provider, so a missing profile
	// or an unreachable server does not cost a long read first, and the
	// snapshot is never older than the store it is compared with.
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

	// With no owned shapes nothing is stale: backfill never deletes.
	diff := snapshot.Compare(run.Desired, stored, nil)
	existing := len(run.Desired) - len(diff.Missing) - len(diff.Changed)
	label := run.Provider.Label()
	res := backfillResult{
		snapshotMeta: newSnapshotMeta(storeID, run, dryRun), StoreReadAt: storeReadAt.UTC(),
		Tuples: tuplesOut(run.Desired), Missing: tuplesOut(diff.Missing), Changed: tuplesOut(diff.Changed), Existing: existing,
	}
	if !machine {
		storeReadLine(out, run, storeLbl, storeReadAt, len(stored))
		snapshotHeader(out, label, run.Result)
		if len(run.Desired) == 0 {
			if err := renderTuples(out, run.Desired); err != nil {
				return err
			}
		} else {
			summary := fmt.Sprintf("%s from %s: %d missing from the store, %d already there",
				mapping.Plural(len(run.Desired), "tuple"), output.SanitizeField(label), len(diff.Missing), existing)
			if len(diff.Changed) > 0 {
				summary += fmt.Sprintf(", %d there with a different condition", len(diff.Changed))
			}
			if _, err := fmt.Fprintln(out, summary); err != nil {
				return err
			}
			if err := renderSections(out, []tupleSection{
				{"missing", "in " + output.SanitizeField(label) + ", not in the store", diff.Missing},
				{"changed", "condition differs", diff.Changed},
			}); err != nil {
				return err
			}
		}
		if len(diff.Changed) > 0 {
			verb, pronoun := "is", "it"
			if len(diff.Changed) > 1 {
				verb, pronoun = "are", "them"
			}
			output.Notef(errw, "%s %s in the store with a different condition; backfill leaves %s, \"ofga mappings drift\" replaces %s",
				mapping.Plural(len(diff.Changed), "tuple"), verb, pronoun, pronoun)
		}
	}
	if len(run.Desired) == 0 {
		if err := emitBackfill(out, c.cli, res); err != nil {
			return err
		}
		output.Successf(errw, "nothing to write")
		return nil
	}
	if dryRun {
		if err := emitBackfill(out, c.cli, res); err != nil {
			return err
		}
		if !machine {
			output.Notef(errw, "dry run: nothing written")
		}
		return nil
	}
	if len(diff.Missing) == 0 {
		if err := emitBackfill(out, c.cli, res); err != nil {
			return err
		}
		_, finish := snapshotTimes(run.Result)
		differ := ""
		if len(diff.Changed) > 0 {
			differ = fmt.Sprintf(", %d there with a different condition", len(diff.Changed))
		}
		output.Successf(errw, "nothing to write: %s already in store %s%s (%s state as of %s UTC)", mapping.Plural(existing, "tuple"),
			output.SanitizeField(storeLbl), differ, output.SanitizeField(label), finish)
		return nil
	}
	// The age belongs above a question, so only a run that will ask prints it.
	if !force && !c.cli.NoInput && stdinIsTerminal(cmd) {
		output.Notef(errw, "%s", snapshotAge(run.Result))
	}
	if err := prompt.Confirm(cmd, backfillQuestion(len(diff.Missing), storeNm, storeID), force); err != nil {
		return err
	}

	sent, err := snapshot.Write(ctx, storeClient{cl}, nil, diff.Missing, func(done, total int) {
		if total > backfillRequestSize { // more than one request
			output.Progressf(errw, "sent %d/%d writes", done, total)
		}
	})
	res.Sent = sentOut{Writes: sent.Writes, Deletes: sent.Deletes}
	if err != nil {
		if eerr := emitBackfill(out, c.cli, res); eerr != nil {
			return eerr
		}
		return reportBackfillFailure(ctx, errw, machine, sent, len(diff.Missing), err)
	}
	if err := emitBackfill(out, c.cli, res); err != nil {
		return err
	}
	_, finish := snapshotTimes(run.Result)
	output.Successf(errw, "wrote %s to store %s; %d already there (%s state as of %s UTC)", mapping.Plural(sent.Writes, "tuple"),
		output.SanitizeField(storeLbl), existing, output.SanitizeField(label), finish)
	return nil
}

// backfillRequestSize is how many changes one write request carries.
const backfillRequestSize = 100

// backfillQuestion is the confirmation prompt.
func backfillQuestion(n int, name, id string) string {
	return fmt.Sprintf("Write %s to store %s?", mapping.Plural(n, "tuple"), storeTarget(name, id))
}

// storeTarget names the store in a question: "name (id)", or the id alone.
func storeTarget(name, id string) string {
	target := output.SanitizeField(id)
	if name != "" {
		target = fmt.Sprintf("%s (%s)", output.SanitizeField(name), target)
	}
	return target
}

// renderShapes prints how many tuples there are and how many of each shape.
func renderShapes(w io.Writer, ts []language.Tuple) error {
	if _, err := fmt.Fprintln(w, mapping.Plural(len(ts), "tuple")); err != nil || len(ts) == 0 {
		return err
	}
	var rows [][]string
	for _, sc := range snapshot.ByShape(ts) {
		rows = append(rows, []string{output.SanitizeField(sc.Shape.String()), strconv.Itoa(sc.N)})
	}
	return output.Table(w, []string{"SHAPE", "TUPLES"}, rows)
}

// renderTuples prints the shape counts and the tuples themselves, at most
// maxPlanLines of them.
func renderTuples(w io.Writer, ts []language.Tuple) error {
	if err := renderShapes(w, ts); err != nil || len(ts) == 0 {
		return err
	}
	field := output.SanitizeField
	if output.Plain {
		field = output.PlainField
	}
	for _, t := range ts[:min(len(ts), maxPlanLines)] {
		if _, err := fmt.Fprintln(w, mapping.ChangeLine(t, field)); err != nil {
			return err
		}
	}
	if more := len(ts) - maxPlanLines; more > 0 {
		_, err := fmt.Fprintf(w, "… %d more; -o json lists them all\n", more)
		return err
	}
	return nil
}

// emitBackfill prints the machine payload for -o json/yaml, or key/values in
// plain mode. Human mode has already printed everything.
func emitBackfill(w io.Writer, c *cli.CLI, res backfillResult) error {
	switch {
	case c.JSON || c.YAML:
		return output.Emit(w, c.YAML, res)
	case output.Plain:
		return output.KeyValues(w, [][2]string{
			{"store", res.Store},
			{"provider", res.Provider},
			{"dry_run", strconv.FormatBool(res.DryRun)},
			{"tuples", strconv.Itoa(len(res.Tuples))},
			{"missing", strconv.Itoa(len(res.Missing))},
			{"changed", strconv.Itoa(len(res.Changed))},
			{"existing", strconv.Itoa(res.Existing)},
			{"writes", strconv.Itoa(res.Sent.Writes)},
		})
	}
	return nil
}

// reportBackfillFailure says how far the write got and returns the error to
// exit with. Cancellation hands its report to main; any other failure is
// printed here, once.
func reportBackfillFailure(ctx context.Context, w io.Writer, machine bool, sent snapshot.Sent, total int, err error) error {
	text := fmt.Sprintf("sent %d of %s before the failure; run the command again to send the rest", sent.Writes, mapping.Plural(total, "write"))
	return reportSnapshotFailure(ctx, w, machine, text, sent, backfillHints(err), err)
}

// reportSnapshotFailure is reportBackfillFailure with its own summary text and
// hints, for the commands that also delete.
func reportSnapshotFailure(ctx context.Context, w io.Writer, machine bool, text string, sent snapshot.Sent, extra []string, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return clierr.WithPartialResult(canceledReport{text})
	}
	// A hint may already carry the error (Friendly keeps it as the cause).
	var hints []string
	if !slices.ContainsFunc(extra, func(h string) bool { return strings.Contains(h, err.Error()) }) {
		hints = append(hints, err.Error())
	}
	hints = append(hints, extra...)
	// Under machine output the error envelope main writes is the only thing on
	// stderr; it carries the summary and hints above.
	if !machine {
		output.Errorf(w, "%s", text)
		for _, h := range hints {
			output.Hintf(w, "%s", h)
		}
	}
	// A lost server stays network even after some writes landed: it is a
	// retry, where a rejection partway through is not.
	kind := clierr.Classify(err)
	if sent.Writes+sent.Deletes > 0 && kind != clierr.KindNetwork {
		kind = clierr.KindPartialFailure
	}
	return clierr.WithHint(strings.Join(hints, "; "), clierr.WithKind(kind, clierr.SilentCause(clierr.Code(err), text, err)))
}

// backfillHints explains a failed write beyond its message. backfill only
// adds, so a condition conflict points at drift, which replaces the tuple.
func backfillHints(err error) []string {
	hints := applyHints(err)
	if conditionConflict(err) {
		hints = append(hints, "ofga mappings drift replaces tuples whose condition changed")
	}
	return hints
}
