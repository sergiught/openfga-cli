package mapping

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/openfga/mapper/language"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/mapping/status"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

// guideURL is the docs section on checking for drift and backfilling.
const guideURL = "https://sergiught.github.io/openfga-cli/guide/mappings/#checking-for-drift-and-backfilling"

// snapshotRun is what both commands have after the provider read and replay.
type snapshotRun struct {
	File     *mapping.File
	Provider snapshot.Provider
	Types    []snapshot.Type
	Result   *snapshot.Result
	Desired  snapshot.Tuples
	// Work is the run's work file; it holds Desired, the store read and the diff.
	Work *workfile.File
	// Contested are tuples set aside from Desired because the replay cannot
	// vouch for them: an earlier event removed them and a later one wrote them,
	// another event of their writer's type removed them, or, under --since, a
	// rule on a windowed type could have removed them for a user that was not
	// read.
	Contested []snapshot.Contested
	// Since is the --since time, zero without it. Windowed names the requested
	// types read by window, empty without --since.
	Since    time.Time
	Windowed []string
	// Resumed is set when some of the read came from an earlier run. StoreSaved
	// says the store read of that run was kept, so this run did not read the
	// store before the provider. Earlier is how many changes of each kind the
	// earlier runs got into the store.
	Resumed, StoreSaved bool
	Earlier, Skip       map[workfile.DiffKind]int
}

// sent is what the earlier runs wrote and deleted: a missing tuple is one
// write, a changed one a delete and a write, a stale one a delete.
func (r *snapshotRun) sent() snapshot.Sent {
	m, c, s := r.Earlier[workfile.Missing], r.Earlier[workfile.Changed], r.Earlier[workfile.Stale]
	return snapshot.Sent{Writes: m + c, Deletes: c + s}
}

// runSnapshot loads and compiles the mapping, resolves --provider, rejects
// unknown snapshot names and mappings with no snapshot rules, then asks the
// provider for the requested types and replays the result.
// beforeRead, when non-nil, runs after that and before the provider is
// contacted, with the loaded mapping file, the provider's types and the
// requested types read by window. Both commands resolve the store
// there so a missing profile fails before the long provider read; drift also
// reads the store there, keeping only the tuples the mapping writes.
// live returns the status card to draw the provider read and the replay on,
// nil for none; it is asked after beforeRead, which may have created it. The
// caller owns it and closes it.
func (c *Command) runSnapshot(cmd *cobra.Command, path, provider string, since time.Time, opts snapshotOpts, live func() *status.Live, beforeRead func(f *mapping.File, types []snapshot.Type, windowed []string) (storeID string, err error), afterOpen func(wf *workfile.File) error) (*snapshotRun, error) {
	machine := c.cli.JSON || c.cli.YAML
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	// Problem lines are not the machine payload: keep stdout clean for jq.
	problemsOut := out
	if machine {
		problemsOut = errw
	}

	if provider == "" {
		return nil, clierr.WithCode(clierr.CodeUsage, clierr.WithHint(
			"--provider takes one of: "+strings.Join(providerNames(), ", "),
			fmt.Errorf("--provider is required")))
	}
	entry, ok := providers[provider]
	if !ok {
		return nil, clierr.WithCode(clierr.CodeUsage, clierr.WithHint(
			"--provider takes one of: "+strings.Join(providerNames(), ", "),
			fmt.Errorf("unknown provider %q", provider)))
	}

	f, problems, err := loadMappingFile(path)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, failToLoad(problemsOut, machine, path, problems)
	}
	m, diags, err := mapping.CompileSource(f.Src)
	if err != nil {
		return nil, failToLoad(problemsOut, machine, path, mapping.FindingsFrom(diags))
	}
	types := entry.Types()
	typeNames := make([]string, len(types))
	for i, t := range types {
		typeNames[i] = t.Name
	}

	if unknown := snapshot.Unknown(f.Doc, types); len(unknown) > 0 {
		summary := fmt.Sprintf("%s names snapshot types %s does not have: %s", path, entry.Label, strings.Join(unknown, ", "))
		hint := entry.Label + " has: " + strings.Join(typeNames, ", ")
		if !machine {
			output.Errorf(problemsOut, "%s", summary)
			output.Hintf(problemsOut, "%s", hint)
		}
		return nil, clierr.WithHint(hint, clierr.WithKind(clierr.KindValidation, clierr.Silent(clierr.CodeError, summary)))
	}
	requested := snapshot.Requested(f.Doc, types)
	if len(requested) == 0 {
		return nil, noSnapshotRules(problemsOut, machine, path, f, types, typeNames)
	}
	var windowed []string
	if !since.IsZero() {
		if windowed, err = windowedRequested(entry.Label, path, types, requested); err != nil {
			return nil, err
		}
	}

	// Building the provider does no network I/O, so a missing credential fails
	// before beforeRead spends a store read.
	p, err := entry.New(cmd)
	if err != nil {
		return nil, err
	}
	// Nor does it cost a request to learn that there is nowhere to keep the work
	// file.
	if opts.state.path == "" {
		if _, err := cacheDir(); err != nil {
			return nil, noCacheDir(err)
		}
	}
	storeID, err := beforeRead(f, types, windowed)
	if err != nil {
		return nil, err
	}

	// From here a failure keeps the work file.
	workPath, err := workPath(opts.state, path, storeID, opts.command)
	if err != nil {
		return nil, err
	}
	meta := workMeta(opts.command, p.Name(), p.Source(), storeID, opts.sinceText, since, requested, f.Src)
	var (
		wf            *workfile.File
		resumed       bool
		storeSaved    bool
		earlier, skip map[workfile.DiffKind]int
	)
	if opts.state.resume {
		if wf, err = openResume(workPath, meta); err != nil {
			return nil, err
		}
		opts.ws.f = wf
		output.Notef(errw, "resuming from %s", workPath)
		// The window the first run started is the one this run reads: --since 24h
		// does not slide an hour on.
		since = wf.Meta().Since
		if earlier, skip, resumed, storeSaved, err = prepareResume(ctx, wf, afterOpen != nil); err != nil {
			return nil, workErr(workPath, err)
		}
	} else if wf, err = createWork(cmd, workPath, meta); err != nil {
		return nil, err
	} else {
		opts.ws.f = wf
	}
	if afterOpen != nil && !storeSaved {
		if err := afterOpen(wf); err != nil {
			return nil, err
		}
	}

	lv := live()
	lv.Provider(entry.Label, p.Stats)
	lv.Phase(entry.Label)
	save := func(u snapshot.Unit, events []map[string]any, finished bool) error {
		return workErr(workPath, wf.Save(u, events, finished))
	}
	res, err := p.Snapshot(ctx, snapshot.Request{
		Types: requested, Since: since,
		Save: save, Done: wf.UnitDone, Remember: wf.Remember, Recall: wf.Recall,
	}, func(pr snapshot.Progress) {
		switch {
		case lv != nil:
			if pr.Step != "" {
				lv.Set(pr.Step, pr.Done, pr.Total)
			}
		case pr.Message != "":
			output.Progressf(errw, "%s", pr.Message)
		}
	})
	if err != nil {
		return nil, err
	}
	if resumed {
		// A copy: the provider's own result is left as it gave it.
		done := *res
		done.Resumed = true
		if started, finished := wf.Window(); !started.IsZero() {
			done.StartedAt, done.FinishedAt = started, finished
		}
		res = &done
	}
	items := 0
	for _, n := range res.Counts {
		items += n.N
	}
	// A provider that ignored Save gave its events in the result: keep them in
	// the work file as one unit, so the replay below reads one place.
	if len(res.Events) > 0 {
		for events := res.Events; len(events) > 0; {
			n := min(len(events), saveChunk)
			if err := save(snapshot.Unit{Key: "all"}, events[:n], n == len(events)); err != nil {
				return nil, err
			}
			events = events[n:]
		}
		// A copy, so the events are not kept by this run and the provider's own
		// result is left as it gave it.
		done := *res
		done.Events = nil
		res = &done
	}

	replay := snapshot.NewReplayWith(m, wf.TupleSet(), wf.PlanState())
	if len(windowed) > 0 {
		replay.Window(windowed, snapshot.UnreadRemovals(f.Doc, windowed))
	}
	var addErr error
	replayed := 0
	_, err = wf.Replay(ctx, replayPage, func(events []map[string]any) error {
		if addErr = replay.Add(ctx, events); addErr != nil {
			return addErr
		}
		replayed += len(events)
		if lv != nil {
			lv.Set("events", replayed, 0)
		}
		return nil
	})
	if err != nil {
		if err == addErr {
			return nil, wf.Named(err)
		}
		return nil, workErr(workPath, err)
	}
	contested, err := replay.DoneEach(ctx, func(t language.Tuple) error { return wf.AddDesired(ctx, t) })
	if err == nil {
		err = wf.FlushDesired(ctx)
	}
	if err != nil {
		return nil, wf.Named(err)
	}
	n, err := wf.DesiredCount()
	if err != nil {
		return nil, workErr(workPath, err)
	}
	// The count is known now, so reports never ask the work file for it.
	desired := counted{wf.Desired(), n}
	lv.Note(thousands(items) + " items, " + thousands(n) + " tuples from " + thousands(replay.Events()) + " events")
	return &snapshotRun{File: f, Provider: p, Types: types, Result: res, Desired: desired, Work: wf, Contested: contested, Since: since, Windowed: windowed,
		Resumed: resumed, StoreSaved: storeSaved, Earlier: earlier, Skip: skip}, nil
}

// replayPage is how many saved events the replay reads at a time; saveChunk is
// how many of a provider's returned events go into the work file in one
// transaction.
const (
	replayPage = 1000
	saveChunk  = 5000
)

// snapshotOpts is what runSnapshot needs to keep the work file.
type snapshotOpts struct {
	command   string // "backfill" or "drift"
	sinceText string // --since as given
	state     *stateFlags
	ws        *workState
}

// windowedRequested returns the requested types the provider can read by
// window, or a usage error when --since would narrow nothing.
func windowedRequested(label, path string, types []snapshot.Type, requested []string) ([]string, error) {
	var all, windowed []string
	for _, t := range types {
		if !t.Windowed {
			continue
		}
		all = append(all, t.Name)
		if slices.Contains(requested, t.Name) {
			windowed = append(windowed, t.Name)
		}
	}
	switch {
	case len(all) == 0:
		return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--since is not supported by %s", label))
	case len(windowed) == 0:
		return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"--since narrows only %s, and %s has no rule for them", strings.Join(all, ", "), path))
	}
	return windowed, nil
}

// noSnapshotRules reports a mapping with nothing to check, suggesting the
// clauses that would opt its rules in.
func noSnapshotRules(w io.Writer, machine bool, path string, f *mapping.File, types []snapshot.Type, typeNames []string) error {
	summary := path + " has no snapshot rules, so there is nothing to check"
	var lines []string
	if sugg := snapshot.Suggestions(f.Doc, types); len(sugg) > 0 {
		width := 0
		for _, s := range sugg {
			width = max(width, utf8.RuneCountInString(s.Rule))
		}
		lines = append(lines, "these rules can opt in by adding a clause to `when`:")
		for _, s := range sugg {
			rule := output.SanitizeField(s.Rule)
			lines = append(lines, fmt.Sprintf("  %s%s %s", rule, strings.Repeat(" ", width-utf8.RuneCountInString(s.Rule)), output.SanitizeField(s.Clause)))
		}
		lines = append(lines, "see "+guideURL)
	} else {
		lines = append(lines, "add a rule whose when names one of: "+strings.Join(typeNames, ", "))
	}
	if !machine {
		cross := lipgloss.NewStyle().Foreground(style.Red).Render(style.IconCross)
		_, _ = fmt.Fprintf(w, "%s %s\n", cross, output.SanitizeField(summary))
		for _, l := range lines {
			_, _ = fmt.Fprintf(w, "  %s\n", l)
		}
	}
	return clierr.WithHint(strings.Join(lines, "; "), clierr.WithKind(clierr.KindValidation, clierr.Silent(clierr.CodeError, summary)))
}

// Times print as "15:04:05"; a read that crosses midnight UTC carries dates.
const (
	clockLayout     = "15:04:05"
	dateClockLayout = "2006-01-02 15:04:05"
)

// snapshotTimes formats when the read started and finished, in UTC and without
// the zone name.
func snapshotTimes(r *snapshot.Result) (start, finish string) {
	layout := snapshotLayout(r)
	return r.StartedAt.UTC().Format(layout), r.FinishedAt.UTC().Format(layout)
}

// snapshotLayout is the layout every time in a report uses: dated when the
// provider read crossed midnight UTC, a clock time otherwise.
func snapshotLayout(r *snapshot.Result) string {
	if r.StartedAt.UTC().Format(time.DateOnly) != r.FinishedAt.UTC().Format(time.DateOnly) {
		return dateClockLayout
	}
	return clockLayout
}

// resumedMark ends the snapshot time line of a run that continued an earlier
// one.
const resumedMark = " (resumed)"

// snapshotHeader prints which provider was read, when, from where, and how
// many events of each type it produced. A count read by window says since when.
func snapshotHeader(w io.Writer, label string, r *snapshot.Result, since time.Time) {
	start, finish := snapshotTimes(r)
	waits := ""
	if r.RateLimited > 0 {
		waits = "  " + mapping.Plural(r.RateLimited, "rate-limit wait")
	}
	if r.Resumed {
		waits += resumedMark
	}
	_, _ = fmt.Fprintf(w, "%s read  %s → %s UTC  %s%s\n", output.SanitizeField(label), start, finish, output.SanitizeField(r.Source), waits)
	width := 0
	for _, c := range r.Counts {
		width = max(width, len(c.Type))
	}
	for _, c := range r.Counts {
		window := ""
		if c.Windowed {
			window = "  (changed since " + since.UTC().Format(dateClockLayout) + " UTC)"
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %d%s\n", width, output.SanitizeField(c.Type), c.N, window)
	}
}

// snapshotAge warns that the read is a point in time: seconds below one
// minute, whole minutes after.
func snapshotAge(r *snapshot.Result) string {
	d := max(now().Sub(r.StartedAt), 0)
	age := fmt.Sprintf("%ds", int(d.Seconds()))
	if d >= time.Minute {
		age = fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("Snapshot is %s old; changes since then are not reflected.", age)
}

// tupleOut is a tuple in a command's JSON document.
type tupleOut struct {
	User      string         `json:"user"`
	Relation  string         `json:"relation"`
	Object    string         `json:"object"`
	Condition string         `json:"condition,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// materialize reads a list into its JSON form, never nil.
func materialize(ts snapshot.Tuples) ([]tupleOut, error) {
	out := make([]tupleOut, 0, count(ts))
	if ts == nil {
		return out, nil
	}
	err := ts.Each(func(t language.Tuple) error {
		out = append(out, tupleOut{User: t.User, Relation: t.Relation, Object: t.Object, Condition: t.Condition, Context: t.Context})
		return nil
	})
	return out, err
}

// streamsJSON reports whether the document goes out as -o json, which can
// stream its lists. YAML, JSONL and --jq build the document in memory.
func streamsJSON(c *cli.CLI) bool {
	return c.JSON && !c.YAML && !output.JSONL && output.JQ == ""
}

func tuplesOut(ts []language.Tuple) []tupleOut {
	out := make([]tupleOut, 0, len(ts))
	for _, t := range ts {
		out = append(out, tupleOut{User: t.User, Relation: t.Relation, Object: t.Object, Condition: t.Condition, Context: t.Context})
	}
	return out
}

// snapshotCount is how many events of one type the read produced.
type snapshotCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// snapshotMeta is what a snapshot command's document says about the read.
type snapshotMeta struct {
	Store      string          `json:"store"`
	Provider   string          `json:"provider"`
	Source     string          `json:"source"`
	StartedAt  time.Time       `json:"snapshot_started_at"`
	FinishedAt time.Time       `json:"snapshot_finished_at"`
	Counts     []snapshotCount `json:"counts"`
	DryRun     bool            `json:"dry_run"`
	Since      *time.Time      `json:"since,omitempty"`
}

func newSnapshotMeta(storeID string, run *snapshotRun, dryRun bool) snapshotMeta {
	meta := snapshotMeta{
		Store: storeID, Provider: run.Provider.Name(), Source: run.Result.Source,
		StartedAt: run.Result.StartedAt.UTC(), FinishedAt: run.Result.FinishedAt.UTC(),
		Counts: make([]snapshotCount, 0, len(run.Result.Counts)), DryRun: dryRun,
	}
	for _, c := range run.Result.Counts {
		meta.Counts = append(meta.Counts, snapshotCount{Type: c.Type, Count: c.N})
	}
	if !run.Since.IsZero() {
		since := run.Since.UTC()
		meta.Since = &since
	}
	return meta
}
