package mapping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

type applyResult struct {
	Store         string        `json:"store"`
	DryRun        bool          `json:"dry_run"`
	FromEvent     int           `json:"from_event,omitempty"`
	Events        int           `json:"events"`
	Unmatched     int           `json:"unmatched"`
	Writes        int           `json:"writes"`
	Deletes       int           `json:"deletes"`
	AppliedEvents int           `json:"applied_events"`
	Changes       []applyChange `json:"changes"`
}

type applyChange struct {
	Event     int            `json:"event"`
	Action    string         `json:"action"`
	User      string         `json:"user"`
	Relation  string         `json:"relation"`
	Object    string         `json:"object"`
	Condition string         `json:"condition,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// maxPlanLines caps the change lines the human plan prints.
const maxPlanLines = 50

// progressEvery throttles progress lines so a big file does not flood stderr.
const progressEvery = 50

func (c *Command) newApplyCmd() *cobra.Command {
	var (
		events        string
		fromEvent     int
		dryRun, force bool
	)
	cmd := &cobra.Command{
		Use:   "apply [file] --events <file|->",
		Short: "Evaluate events and write the resulting tuples to the store",
		Long: "Evaluate events with a mapping file and write the tuples they produce to the active profile's store. With no file argument it uses ./mapping.yaml.\n\n" +
			"Events are a JSON object, a JSON array, or one object per line (JSONL); --events - reads stdin. " +
			"apply always plans first: it reads the store for every tuple filter, shows each event's writes and " +
			"deletes, and asks before writing (--force skips the question, --dry-run stops after the plan). " +
			"It then writes exactly that plan. Each event goes out as one request, except events with more " +
			"than 100 changes or a tuple whose condition or context changes (deleted, then rewritten), which are " +
			"split into several. Writes of tuples that already exist unchanged and deletes of tuples already gone " +
			"are ignored; a write that conflicts with a stored tuple's condition fails — use a tuple_filter for " +
			"tuples whose condition or context changes. The plan reflects the store when it was computed.\n\n" +
			"Re-running the whole file replays every event, and a patch or delete rule in an earlier event can " +
			"briefly revoke tuples a later event re-adds. Resume a failed run with --from-event, which the failure " +
			"message prints.\n\n" +
			"Exit codes: 0 ok · 1 error · 2 usage · 4 network · 130 interrupted.",
		Example: `# See what an export would change
ofga mappings apply --events export.jsonl --dry-run

# Apply it without the prompt, in a script
ofga mappings apply sync/auth0.yaml --events export.jsonl --force

# Continue a run that failed at event 40
ofga mappings apply sync/auth0.yaml --events export.jsonl --from-event 40 --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runApply(cmd, targetPath(args), events, fromEvent, dryRun, force)
		},
	}
	cmd.Flags().StringVar(&events, "events", "", "events to apply: a JSON or JSONL file, or - for stdin")
	cmd.Flags().IntVar(&fromEvent, "from-event", 1, "skip the events before this one (1-based), to resume a failed run")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the plan without writing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write without asking")
	_ = cmd.MarkFlagRequired("events")
	return cmd
}

func (c *Command) runApply(cmd *cobra.Command, path, eventsPath string, fromEvent int, dryRun, force bool) error {
	machine := c.cli.JSON || c.cli.YAML
	if machine && !dryRun && !force {
		return clierr.WithCode(clierr.CodeUsage, errors.New("-o json/yaml never prompts; pass --dry-run or --force"))
	}
	// Piped events use up stdin, so nothing could answer the prompt: refuse
	// now rather than after reading them and planning against the store.
	if eventsPath == "-" && !dryRun && !force && !stdinIsTerminal(cmd) {
		return prompt.ErrNeedsForce
	}
	ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	f, problems, err := loadMappingFile(path)
	if err != nil {
		return err
	}
	// Problem lines are not the machine payload: keep stdout clean for jq.
	problemsOut := out
	if machine {
		problemsOut = errw
	}
	if f == nil {
		return failToLoad(problemsOut, machine, path, problems)
	}
	m, diags, err := mapping.CompileSource(f.Src)
	if err != nil {
		return failToLoad(problemsOut, machine, path, mapping.FindingsFrom(diags))
	}
	events, err := readEvents(cmd, eventsPath)
	if err != nil {
		return err
	}
	if fromEvent < 1 || fromEvent > len(events) {
		source := eventsPath
		if source == "-" {
			source = "stdin"
		}
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--from-event %d: %s has %s", fromEvent, source, mapping.Plural(len(events), "event")))
	}

	cl, r, err := c.cli.ClientWithStore()
	if err != nil {
		return err
	}
	// The name is a courtesy, so a store we may not read (403/404) just shows
	// its id. An unreachable server or failed auth would fail every later
	// request, so stop before planning rather than offer a prompt we cannot honour.
	name := ""
	s, serr := cl.Stores.Get(ctx, r.StoreID)
	switch {
	case serr == nil:
		name = s.Name
	case ctx.Err() != nil || clierr.IsConnErr(serr) || clierr.Friendly(serr) != serr.Error():
		return serr
	}
	label := name
	if label == "" {
		label = r.StoreID
	}

	plan, err := mapping.PlanEvents(ctx, m, events, fromEvent, storeClient{cl}, func(done, total int) {
		if total >= progressEvery && (done%progressEvery == 0 || done == total) {
			output.Progressf(errw, "planning event %d/%d", done, total)
		}
	})
	if err != nil {
		return err
	}

	res := newApplyResult(r.StoreID, plan, dryRun)
	if fromEvent > 1 {
		res.FromEvent = fromEvent
	}
	if !machine {
		if err := renderPlan(out, plan); err != nil {
			return err
		}
	}
	output.Infof(errw, "%s", mapping.PlanSummary(plan))

	if plan.Writes+plan.Deletes == 0 {
		if err := emitApply(out, c.cli, res); err != nil {
			return err
		}
		output.Successf(errw, "nothing to change")
		return nil
	}
	if dryRun {
		return emitApply(out, c.cli, res)
	}
	if err := prompt.Confirm(cmd, applyQuestion(plan.Writes, plan.Deletes, name, r.StoreID), force); err != nil {
		return err
	}

	applied, err := mapping.Execute(ctx, plan, storeClient{cl}, func(done, total int) {
		if total >= progressEvery && (done%progressEvery == 0 || done == total) {
			output.Progressf(errw, "applying event %d/%d", done, total)
		}
	})
	res.AppliedEvents = applied
	if err != nil {
		if eerr := emitApply(out, c.cli, res); eerr != nil {
			return eerr
		}
		// A run from the start that got nothing through resumes by running it
		// again, so a --from-event line would only be noise.
		resume := ""
		if applied > 0 || fromEvent > 1 {
			resume = resumeHint(cmd, eventsPath, resumeCommand(cmd, path, eventsPath, plan.Events[applied].Index, force))
		}
		return reportApplyFailure(ctx, errw, machine, plan, applied, err, resume)
	}
	if err := emitApply(out, c.cli, res); err != nil {
		return err
	}
	// "sent", not "wrote": the server ignores writes that already exist and
	// deletes already gone without saying which.
	output.Successf(errw, "%s", mapping.SentLine(plan, output.SanitizeField(label)))
	return nil
}

// resumeCommand is the command line that continues an interrupted apply at
// event next. It repeats --force and any store-selecting flags given, so the
// rest of the events land where the first ones did.
func resumeCommand(cmd *cobra.Command, path, eventsPath string, next int, force bool) string {
	flags := resumeFlags(cmd)
	if force {
		flags = append([]string{"--force"}, flags...)
	}
	return resumeLine(path, eventsPath, next, flags)
}

// resumeFlags is the store-selecting flags the user gave, as they would be
// repeated on a resume command line.
func resumeFlags(cmd *cobra.Command) []string {
	var flags []string
	for _, name := range []string{"config", "profile", "api-url", "store-id", "model-id"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			v := f.Value.String()
			if name == "api-url" {
				v = cli.RedactedURL(v)
			}
			flags = append(flags, "--"+name, shellQuote(v))
		}
	}
	return flags
}

// resumeLine is the command that continues applying path's events at event
// from, followed by flags. An empty eventsPath prints "EVENTS_FILE".
func resumeLine(path, eventsPath string, from int, flags []string) string {
	if eventsPath == "" {
		eventsPath = "EVENTS_FILE"
	} else {
		eventsPath = shellQuote(eventsPath)
	}
	parts := []string{"ofga mappings apply", shellQuote(path), "--events", eventsPath, "--from-event", strconv.Itoa(from)}
	return strings.Join(append(parts, flags...), " ")
}

// resumeHint wraps the resume command in what it cannot carry: --from-event
// counts events, so stdin must deliver the same ones again, and request
// headers and auth files are left out because they can hold secrets.
func resumeHint(cmd *cobra.Command, eventsPath, command string) string {
	hint := "resume with: " + command
	if eventsPath == "-" {
		hint = "resume by feeding the same events to: " + command
	}
	for _, name := range []string{"header", "auth-token-file", "auth-client-secret-file", "auth-private-key-file"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return hint + " (plus the --header/--auth-* flags you used)"
		}
	}
	return hint
}

// shellQuote single-quotes s for a POSIX shell unless it is plainly safe.
func shellQuote(s string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@%+=,"
	if s != "" && strings.Trim(s, safe) == "" { // every byte is in safe
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// readEvents opens and parses the --events source: a file, or stdin for "-".
func readEvents(cmd *cobra.Command, path string) ([]map[string]any, error) {
	if path == "-" {
		if stdinIsTerminal(cmd) {
			output.Notef(cmd.ErrOrStderr(), "reading events from stdin (ctrl+d to end)")
		}
		data, err := readAll(cmd.Context(), cmd.InOrStdin())
		if err != nil {
			return nil, err
		}
		return mapping.ParseEvents(bytes.NewReader(data), "stdin")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("events file %s not found", path)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return mapping.ParseEvents(f, path)
}

// readAll reads r to EOF, or until ctx is done. A terminal or a pipe left open
// may never reach EOF, and Ctrl-C only cancels ctx, so the read runs aside and
// is abandoned on cancel: the command returns, and the process exits with it.
func readAll(ctx context.Context, r io.Reader) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		done <- result{data, err}
	}()
	select {
	case res := <-done:
		return res.data, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func stdinIsTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// reportApplyFailure says how far apply got, ending with the resume command
// when there is one, and returns the error to exit with. applied counts the
// plan's events that landed; event numbers are the file's. A cancellation
// hands its report to main (which prints "canceled" and the report as a hint);
// any other failure is printed here, once.
func reportApplyFailure(ctx context.Context, w io.Writer, machine bool, plan *mapping.Plan, applied int, err error, resume string) error {
	failed := plan.Events[applied].Index
	msg := strings.TrimPrefix(err.Error(), fmt.Sprintf("event %d: ", failed))
	done := mapping.AppliedSoFar(plan, applied)
	// Resuming at a partly written split event is still right: its deletes
	// plan again as no-ops and its writes go out.
	split := !plan.Events[applied].Atomic
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		text := fmt.Sprintf("%s; stopped before event %d finished", done, failed)
		if split {
			text += " (it may be partly written)"
		}
		if resume != "" {
			text += "\n  " + resume
		}
		return clierr.WithPartialResult(canceledReport{text})
	}
	summary := fmt.Sprintf("%s; event %d failed: %s", done, failed, msg)
	var hints []string
	if split {
		hints = append(hints, fmt.Sprintf("event %d goes out in several requests and may be partly written", failed))
	}
	hints = append(hints, applyHints(err)...)
	if resume != "" {
		hints = append(hints, resume)
	}
	// Under machine output the error envelope main writes is the only thing on
	// stderr; it carries the summary and hints above.
	if !machine {
		output.Errorf(w, "%s", summary)
		for _, h := range hints {
			output.Hintf(w, "%s", h)
		}
	}
	// A lost server stays network even after some events landed: it is a
	// retry, where a rejection partway through is not.
	kind := clierr.Classify(err)
	if applied > 0 && kind != clierr.KindNetwork {
		kind = clierr.KindPartialFailure
	}
	return clierr.WithHint(strings.Join(hints, "; "), clierr.WithKind(kind, clierr.SilentCause(clierr.Code(err), summary, err)))
}

// applyHints explains a failed apply beyond its message: the friendly text
// when it differs from the raw one, then the condition-conflict hint.
func applyHints(err error) []string {
	var hints []string
	if friendly := clierr.Friendly(err); friendly != err.Error() {
		hints = append(hints, friendly)
	}
	if conditionConflict(err) {
		hints = append(hints, "a tuple with that user, relation and object already exists with a different condition; "+
			"a rule without a tuple_filter cannot replace it — add a tuple_filter, or delete the old tuple first")
	}
	return hints
}

// conditionConflict reports the server refusing to write a tuple that already
// exists with a different condition. The SDK has no type for a 409, and a 409
// alone is any transactional conflict, so the message decides.
func conditionConflict(err error) bool {
	var er *openfga.ErrorResponse
	return errors.As(err, &er) && er.StatusCode() == http.StatusConflict &&
		strings.Contains(er.Message, "already exists with a different condition")
}

// canceledReport is the interruption report; it still is a context.Canceled.
type canceledReport struct{ text string }

func (e canceledReport) Error() string { return e.text }
func (e canceledReport) Unwrap() error { return context.Canceled }

func newApplyResult(storeID string, plan *mapping.Plan, dryRun bool) applyResult {
	res := applyResult{
		Store: storeID, DryRun: dryRun, Events: len(plan.Events), Unmatched: plan.Unmatched,
		Writes: plan.Writes, Deletes: plan.Deletes, Changes: []applyChange{},
	}
	for _, ep := range plan.Events {
		for _, t := range ep.Changes {
			res.Changes = append(res.Changes, applyChange{
				Event: ep.Index, Action: string(t.Action), User: t.User, Relation: t.Relation,
				Object: t.Object, Condition: t.Condition, Context: t.Context,
			})
		}
	}
	return res
}

// emitApply prints the machine payload for -o json/yaml, or key/values in
// plain mode. Human mode has already printed the plan and prints nothing here.
func emitApply(w io.Writer, c *cli.CLI, res applyResult) error {
	switch {
	case c.JSON || c.YAML:
		return output.Emit(w, c.YAML, res)
	case output.Plain:
		return output.KeyValues(w, [][2]string{
			{"store", res.Store},
			{"dry_run", strconv.FormatBool(res.DryRun)},
			{"events", strconv.Itoa(res.Events)},
			{"unmatched", strconv.Itoa(res.Unmatched)},
			{"writes", strconv.Itoa(res.Writes)},
			{"deletes", strconv.Itoa(res.Deletes)},
			{"applied_events", strconv.Itoa(res.AppliedEvents)},
		})
	}
	return nil
}

// applyQuestion is the confirmation prompt, leaving out a zero half.
func applyQuestion(writes, deletes int, name, id string) string {
	if name != "" {
		name = output.SanitizeField(name)
	}
	return mapping.ApplyQuestion(writes, deletes, name, output.SanitizeField(id))
}

// renderPlan prints each event's changes on stdout, at most maxPlanLines of
// them. Events that changed nothing are not listed; the summary counts them.
func renderPlan(w io.Writer, plan *mapping.Plan) error {
	field := output.SanitizeField
	if output.Plain {
		field = output.PlainField
	}
	shown, total := 0, plan.Writes+plan.Deletes
	skip := false // the current event's header was left out for the cap
	for _, line := range mapping.PlanText(plan, field) {
		if !strings.HasPrefix(line, "  ") { // an event header
			skip = shown >= maxPlanLines
			if skip {
				continue
			}
		} else if skip || shown >= maxPlanLines {
			continue
		} else {
			shown++
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if more := total - shown; more > 0 {
		_, err := fmt.Fprintf(w, "… %d more; --dry-run -o json lists them all\n", more)
		return err
	}
	return nil
}

// changeLine is one plan line; see mapping.ChangeLine.
func changeLine(t language.Tuple, field func(string) string) string {
	return mapping.ChangeLine(t, field)
}
