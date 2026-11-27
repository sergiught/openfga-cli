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

// backfillResult is the document `mappings backfill` prints under -o json/yaml.
type backfillResult struct {
	snapshotMeta
	StoreReadAt time.Time       `json:"store_read_at"`
	Tuples      []tupleOut      `json:"tuples"`
	Missing     []tupleOut      `json:"missing"`
	Changed     []tupleOut      `json:"changed"`
	Existing    int             `json:"existing"`
	NotChecked  []notCheckedOut `json:"not_checked"`
	Sent        sentOut         `json:"sent"`
}

// sentOut is how many changes reached the store.
type sentOut struct {
	Writes  int `json:"writes"`
	Deletes int `json:"deletes"`
}

func (c *Command) newBackfillCmd() *cobra.Command {
	var (
		provider      string
		since         string
		dryRun, force bool
		state         stateFlags
	)
	cmd := &cobra.Command{
		Use:   "backfill [file] --provider <name>",
		Short: "Write the tuples an identity provider's current state maps to",
		Long: "Read an identity provider's current state, replay it through a mapping file, and write the resulting tuples to the active profile's store. With no file argument it uses ./mapping.yaml.\n\n" +
			"The read comes back as snapshot events, one per user, organization, membership and so on, which the mapping's rules handle like live events. " +
			"A rule takes part when its `when` also names a snapshot type, such as `input.type == \"snapshot.organization.member\"`; " +
			"a mapping with none has nothing to backfill, and the error suggests the clause for each rule. " +
			"Only the types the mapping names are read.\n\n" +
			"backfill reads the provider, then the store, and writes only the tuples the store lacks. On a large store it reads only the objects the mapping writes to. It never deletes, and a tuple that is already there with a different condition is left for drift to replace. " +
			"It shows the tuples it would write and asks before writing (--force skips the question, --dry-run stops after showing them). " +
			"The provider is read once, so the tuples reflect it as of that moment. Changes made while the command runs, or after it, are not included. " +
			"Use `ofga mappings drift` to compare the store with the provider and to remove tuples the provider no longer backs.\n\n" +
			"Run again after a failure: tuples that already landed are not written twice. Auth0 needs a machine-to-machine application. From 1,000 users up the read uses Auth0's user export job, which needs no extra scope. " +
			"--since 2h (or an RFC 3339 time) reads only the Auth0 users changed since then, at most 1,000; other types are still read whole.\n\n" +
			"Exit codes: 0 ok · 1 error · 2 usage · 4 network · 130 interrupted.",
		Example: `# See what a backfill from Auth0 would write
ofga mappings backfill auth0.yaml --provider auth0 --dry-run

# Read only the users changed in the last two hours
ofga mappings backfill auth0.yaml --provider auth0 --since 2h --force

# Write it without the prompt, in a script
ofga mappings backfill auth0.yaml --provider auth0 --auth0-client-secret-file ./secret --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runBackfill(cmd, targetPath(args), provider, since, dryRun, force, &state)
		},
	}
	addProviderFlags(cmd, &provider)
	cmd.Flags().StringVar(&since, "since", "", "read only what changed since a duration ago (2h) or an RFC 3339 time; users only")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the tuples without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	state.register(cmd.Flags())
	return cmd
}

// runBackfill runs backfill and then settles the work file by how it ended.
func (c *Command) runBackfill(cmd *cobra.Command, path, provider, since string, dryRun, force bool, state *stateFlags) error {
	ws := &workState{}
	err := c.backfill(cmd, path, provider, since, dryRun, force, state, ws)
	return ws.end(c, cmd, state, err, force, c.cli.JSON || c.cli.YAML)
}

func (c *Command) backfill(cmd *cobra.Command, path, provider, since string, dryRun, force bool, state *stateFlags, ws *workState) error {
	machine := c.cli.JSON || c.cli.YAML
	sinceTime, err := parseSince(since)
	if err != nil {
		return err
	}
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	var (
		cl                *openfga.Client
		storeID           string
		storeNm, storeLbl string
	)
	// The store is resolved before the provider, so a missing profile or an
	// unreachable server does not cost a long read first. It is read after the
	// provider, once the desired tuples say which objects are worth reading.
	meter := new(client.Meter)
	phases := []string{providers[provider].Label, "Store"}
	if !dryRun {
		phases = append(phases, "Write")
	}
	live := c.liveStatus(errw, machine, "ofga mappings backfill", phases...)
	defer live.Close()
	run, err := c.runSnapshot(cmd, path, provider, sinceTime, snapshotOpts{command: "backfill", sinceText: since, state: state, ws: ws}, func() *status.Live { return live }, func(*mapping.File, []snapshot.Type, []string) (string, error) {
		var r config.Resolved
		var err error
		if cl, r, err = c.cli.ClientWithStore(client.WithMeter(meter)); err != nil {
			return "", err
		}
		storeID = r.StoreID
		storeNm, storeLbl, err = storeName(ctx, cl, storeID)
		return storeID, err
	}, nil)
	if err != nil {
		return err
	}
	wf := run.Work
	workPath := wf.Path()

	// With nothing desired there is nothing to look up, so the store is not
	// read and store_read_at stays the zero time.
	var (
		read        storeRead
		storeReadAt time.Time
	)
	nDesired := count(run.Desired)
	if nDesired > 0 {
		live.Phase("Store")
		live.Store(meter)
		if live == nil {
			output.Progressf(errw, "reading the store")
		}
		// The sink keeps every tuple the read sees, not only the wanted ones:
		// backfill owns no shapes, so the extra rows never become stale.
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
		// total counts every read, crowded pairs included: it feeds the report's
		// "read" line and is not a row count.
		read = storeRead{Read: total, Objects: objects}
		if err := wf.MarkStoreRead(now(), total, objects); err != nil {
			return workErr(workPath, err)
		}
		live.Note(matchNote(read))
		storeReadAt = now()
	}
	// Everything the reads have to say comes next, so the card gives way.
	live.Suspend("read")

	// With no owned shapes nothing is stale: backfill never deletes.
	counts, err := wf.Compare(ctx, nil, nil)
	if err != nil {
		return workErr(workPath, err)
	}
	nMissing, nChanged := counts.Missing, counts.Changed
	notChecked := snapshot.Removers(run.File.Doc, run.Contested)
	existing := counts.Desired - nMissing - nChanged
	label := run.Provider.Label()
	res := backfillResult{
		snapshotMeta: newSnapshotMeta(storeID, run, dryRun), StoreReadAt: storeReadAt.UTC(),
		Existing:   existing,
		NotChecked: notCheckedOuts(notChecked),
	}
	desired, dl := run.Desired, diffLists(wf, counts)
	lists := map[string]snapshot.Tuples{"tuples": desired, "missing": dl.Missing, "changed": dl.Changed}
	if !machine {
		report := func() error {
			if output.Plain {
				return renderBackfillPlain(out, run, label, storeLbl, storeReadAt, read, desired, dl, existing, notChecked)
			}
			return renderBackfillReport(out, run, storeLbl, storeReadAt, read.Read, read.Objects, desired, dl, existing, notChecked)
		}
		if err := report(); err != nil {
			return err
		}
		if nChanged > 0 {
			verb, pronoun := "is", "it"
			if nChanged > 1 {
				verb, pronoun = "are", "them"
			}
			output.Notef(errw, "%s %s in the store with a different condition; backfill leaves %s, \"ofga mappings drift\" replaces %s",
				mapping.Plural(nChanged, "tuple"), verb, pronoun, pronoun)
		}
	}
	if nDesired == 0 {
		if err := emitBackfill(out, c.cli, res, lists); err != nil {
			return err
		}
		output.Successf(errw, "nothing to write")
		ws.finished = true
		return nil
	}
	if dryRun {
		if err := emitBackfill(out, c.cli, res, lists); err != nil {
			return err
		}
		if !machine {
			output.Notef(errw, "dry run: nothing written")
		}
		ws.finished = true
		return nil
	}
	if nMissing == 0 {
		if err := emitBackfill(out, c.cli, res, lists); err != nil {
			return err
		}
		_, finish := snapshotTimes(run.Result)
		differ := ""
		if nChanged > 0 {
			differ = fmt.Sprintf(", %d there with a different condition", nChanged)
		}
		output.Successf(errw, "nothing to write: %s already in store %s%s (%s state as of %s UTC)", bold(mapping.Plural(existing, "tuple")),
			output.SanitizeField(storeLbl), differ, output.SanitizeField(label), finish)
		ws.finished = true
		return nil
	}
	if machine && !force {
		// Machine output covers -o json/yaml/jsonl and agent mode, which has no -o.
		// It never prompts, so the result is printed and the write is declined.
		if err := emitBackfill(out, c.cli, res, lists); err != nil {
			return err
		}
		ws.finished = true
		return clierr.WithCode(clierr.CodeUsage, clierr.WithHint("pass --dry-run to preview or --force to write",
			errors.New("non-interactive runs never prompt; pass --dry-run to preview or --force to write")))
	}
	// The age belongs above a question, so only a run that will ask prints it.
	if !force && !c.cli.NoInput && stdinIsTerminal(cmd) {
		output.Notef(errw, "%s", snapshotAge(run.Result))
	}
	if err := prompt.Confirm(cmd, backfillQuestion(nMissing, storeNm, storeID), force); err != nil {
		// A declined question and a missing terminal end the run; an interrupted
		// one does not.
		ws.finished = ctx.Err() == nil
		return err
	}

	live.Phase("Write")
	sent, err := sendPages(wf, workfile.Missing, func(page, _ []language.Tuple, prior int) (snapshot.Sent, error) {
		return snapshot.Write(ctx, storeClient{cl}, nil, page, func(done, _ int) {
			switch {
			case live != nil:
				live.Set("changes", prior+done, nMissing)
			case nMissing > backfillRequestSize: // more than one request
				output.Progressf(errw, "sent %d/%d writes", prior+done, nMissing)
			}
		})
	})
	live.Note(thousands(sent.Writes+sent.Deletes) + " changes")
	live.Suspend("wrote")
	earlier := run.sent()
	res.Sent = sentOut{Writes: sent.Writes + earlier.Writes, Deletes: sent.Deletes + earlier.Deletes}
	if err != nil {
		if eerr := emitBackfill(out, c.cli, res, lists); eerr != nil {
			return eerr
		}
		return reportBackfillFailure(ctx, errw, machine, sent, nMissing, wf.Named(err))
	}
	if err := emitBackfill(out, c.cli, res, lists); err != nil {
		return err
	}
	_, finish := snapshotTimes(run.Result)
	output.Successf(errw, "wrote %s to store %s; %d already there (%s state as of %s UTC)", bold(mapping.Plural(sent.Writes, "tuple")),
		output.SanitizeField(storeLbl), existing, output.SanitizeField(label), finish)
	ws.finished = true
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

// renderBackfillPlain prints the backfill report for --plain.
func renderBackfillPlain(out io.Writer, run *snapshotRun, label, storeLbl string, storeReadAt time.Time, read storeRead, desired snapshot.Tuples, diff snapshot.DiffLists, existing int, notChecked []snapshot.NotChecked) error {
	snapshotHeader(out, label, run.Result, run.Since)
	if count(desired) > 0 {
		storeReadLine(out, run, storeLbl, storeReadAt, read.Read, read.Objects)
	}
	if count(desired) == 0 {
		if err := renderTuples(out, desired, nil); err != nil {
			return err
		}
	} else {
		summary := fmt.Sprintf("%s from %s: %d missing from the store, %d already there",
			mapping.Plural(count(desired), "tuple"), output.SanitizeField(label), count(diff.Missing), existing)
		if n := count(diff.Changed); n > 0 {
			summary += fmt.Sprintf(", %d there with a different condition", n)
		}
		if _, err := fmt.Fprintln(out, summary); err != nil {
			return err
		}
		if err := renderSections(out, []tupleSection{
			{"missing", "in " + output.SanitizeField(label) + ", not in the store", diff.Missing, nil},
			{"changed", "condition differs", diff.Changed, diff.Was},
		}); err != nil {
			return err
		}
	}
	return renderNotChecked(out, notChecked)
}

// renderShapes prints how many tuples there are and how many of each shape.
func renderShapes(w io.Writer, ts snapshot.Tuples) error {
	counts, err := snapshot.CountShapes(ts)
	if err != nil {
		return err
	}
	n := 0
	for _, sc := range counts {
		n += sc.N
	}
	if _, err := fmt.Fprintln(w, mapping.Plural(n, "tuple")); err != nil || n == 0 {
		return err
	}
	var rows [][]string
	for _, sc := range counts {
		rows = append(rows, []string{output.SanitizeField(sc.Shape.String()), strconv.Itoa(sc.N)})
	}
	return output.Table(w, []string{"SHAPE", "TUPLES"}, rows)
}

// renderTuples prints the shape counts and the tuples themselves, at most
// maxPlanLines of them. When was is set, each line ends with the stored
// version's condition.
func renderTuples(w io.Writer, ts, was snapshot.Tuples) error {
	if err := renderShapes(w, ts); err != nil {
		return err
	}
	n := count(ts)
	if n == 0 {
		return nil
	}
	shown, err := firstN(ts, maxPlanLines)
	if err != nil {
		return err
	}
	var shownWas []language.Tuple
	if was != nil && was.Len() > 0 {
		if shownWas, err = firstN(was, maxPlanLines); err != nil {
			return err
		}
	}
	field := output.SanitizeField
	if output.Plain {
		field = output.PlainField
	}
	for i, t := range shown {
		line := mapping.ChangeLine(t, field)
		if shownWas != nil {
			line += " was " + storedCondition(shownWas[i], field)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if more := n - maxPlanLines; more > 0 {
		_, err := fmt.Fprintf(w, "… %d more; -o json lists them all\n", more)
		return err
	}
	return nil
}

// errEnough stops a list read once firstN has what it wants.
var errEnough = errors.New("enough")

// firstN returns the first n tuples of a list, reading no further.
func firstN(ts snapshot.Tuples, n int) ([]language.Tuple, error) {
	var out []language.Tuple
	if n <= 0 {
		return out, nil
	}
	err := ts.Each(func(t language.Tuple) error {
		out = append(out, t)
		if len(out) >= n {
			return errEnough
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	return out, err
}

// storedCondition is the condition of a stored tuple, or "no condition".
func storedCondition(t language.Tuple, field func(string) string) string {
	if c := mapping.ConditionText(t, field); c != "" {
		return c
	}
	return "no condition"
}

// emitBackfill prints the machine payload for -o json/yaml, or key/values in
// plain mode. Human mode has already printed everything.
//
// The result's list fields are left empty: lists holds them by JSON name
// ("tuples", "missing", "changed"). -o json streams them; the other formats
// read them into the result first.
func emitBackfill(w io.Writer, c *cli.CLI, res backfillResult, lists map[string]snapshot.Tuples) error {
	switch {
	case streamsJSON(c):
		return streamJSON(w, res, lists)
	case c.JSON || c.YAML:
		var err error
		if res.Tuples, err = materialize(lists["tuples"]); err != nil {
			return err
		}
		if res.Missing, err = materialize(lists["missing"]); err != nil {
			return err
		}
		if res.Changed, err = materialize(lists["changed"]); err != nil {
			return err
		}
		return output.Emit(w, c.YAML, res)
	case output.Plain:
		return output.KeyValues(w, [][2]string{
			{"store", res.Store},
			{"provider", res.Provider},
			{"dry_run", strconv.FormatBool(res.DryRun)},
			{"tuples", strconv.Itoa(count(lists["tuples"]))},
			{"missing", strconv.Itoa(count(lists["missing"]))},
			{"changed", strconv.Itoa(count(lists["changed"]))},
			{"existing", strconv.Itoa(res.Existing)},
			{"writes", strconv.Itoa(res.Sent.Writes)},
			{"not_checked", strconv.Itoa(len(res.NotChecked))},
		})
	}
	return nil
}

// reportBackfillFailure says how far the write got and returns the error to
// exit with. Cancellation hands its report to main; any other failure is
// printed here, once.
func reportBackfillFailure(ctx context.Context, w io.Writer, machine bool, sent snapshot.Sent, total int, err error) error {
	text := fmt.Sprintf("sent %d of %s before the failure; run again with --resume to send the rest", sent.Writes, mapping.Plural(total, "write"))
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
