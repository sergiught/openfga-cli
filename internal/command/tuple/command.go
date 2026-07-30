// Package tuple implements `ofga tuples`: write, delete, read relationship
// tuples and follow the changelog.
package tuple

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/readlimit"
	"github.com/sergiught/openfga-cli/internal/style"
)

// maxTuplesPerWrite is OpenFGA's default per-request write limit; bulk imports
// are chunked to stay under it.
const maxTuplesPerWrite = 100

// Command is the `tuple` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the tuple command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:     "tuples",
		Aliases: []string{"tuple"},
		RunE:    cli.GroupRunE,
		Short:   "Write, delete and read relationship tuples",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands wires the tuple sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		c.writeCmd(),
		c.deleteCmd(),
		c.readCmd(),
		c.changesCmd(),
	)
}

func (c *Command) writeCmd() *cobra.Command {
	var (
		dryRun                        bool
		file, fileFormat              string
		fUser, fRel, fObj             string
		fCondition, fConditionContext string
		bulk                          bulkOpts
	)
	cmd := &cobra.Command{
		Use:     "write [user] [relation] [object]",
		Aliases: []string{"add", "create"},
		Short:   "Write one relationship tuple, or many with --file",
		Example: `  ofga tuples write user:anne viewer document:roadmap
  ofga tuples write --user user:anne --relation viewer --object document:roadmap
  ofga tuples write user:anne viewer document:roadmap --condition non_expired_grant --condition-context '{"grant_duration":"10m"}'
  ofga tuples write --file tuples.json
  ofga tuples write --file tuples.csv
  cat tuples.jsonl | ofga tuples write --file - --file-format jsonl`,
		Long: "Write one relationship tuple, or many with --file.\n\n" + bulkFileHelp,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fConditionContext != "" && fCondition == "" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--condition-context requires --condition"))
			}
			if file != "" {
				if fCondition != "" {
					return clierr.WithCode(clierr.CodeUsage,
						fmt.Errorf("--condition cannot be combined with --file; set a condition per tuple in the file instead"))
				}
				format, err := resolveBulkFormat(file, fileFormat)
				if err != nil {
					return err
				}
				bulk.format = format
				if err := bulk.validate(false); err != nil {
					return err
				}
				keys, err := bulkTuples(cmd, file, format, args, fUser, fRel, fObj, false)
				if err != nil {
					return err
				}
				if dryRun {
					if c.cli.JSON || c.cli.YAML {
						return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{"dry_run": true, "would_write": len(keys)})
					}
					if output.Plain {
						return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"dry_run", "true"}, {"would_write", fmt.Sprint(len(keys))}})
					}
					output.Infof(cmd.ErrOrStderr(), "would write %d tuple(s)", len(keys))
					return nil
				}
				cl, _, err := c.cli.ClientWithStore()
				if err != nil {
					return err
				}
				return runBulk(cmd, c.cli, cl, keys, false, bulk)
			}
			user, relation, object, err := fga.Triple(args, fUser, fRel, fObj)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			key, err := fga.ParseTuple(user, relation, object)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if fCondition != "" {
				condCtx, err := fga.ParseJSONObject("--condition-context", fConditionContext)
				if err != nil {
					return clierr.WithCode(clierr.CodeUsage, err)
				}
				key.Condition = &openfga.RelationshipCondition{Name: fCondition, Context: condCtx}
			}
			if dryRun {
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{"dry_run": true, "would_write": 1})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"dry_run", "true"}, {"would_write", "1"}})
				}
				output.Infof(cmd.ErrOrStderr(), "would write %s", style.Bold.Render(fga.FormatTuple(key)))
				return nil
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.WriteRequest{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
			if err := cl.Tuples.Write(cmd.Context(), req); err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]int{"written": 1})
			}
			if output.Plain {
				return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"written", "1"}})
			}
			output.Successf(cmd.ErrOrStderr(), "wrote %s", style.Bold.Render(fga.FormatTuple(key)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the tuple that would be written without writing it")
	cmd.Flags().StringVar(&file, "file", "", "file of tuples to write in bulk ('-' for stdin)")
	cmd.Flags().StringVar(&fileFormat, "file-format", "", "format of --file: json|jsonl|yaml|csv (default: inferred from the extension; json for stdin)")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	cmd.Flags().StringVar(&fCondition, "condition", "", "ABAC condition name to attach to the tuple")
	cmd.Flags().StringVar(&fConditionContext, "condition-context", "", "JSON object of condition context parameters (requires --condition)")
	cmd.Flags().StringVar(&bulk.onDuplicate, "on-duplicate", string(openfga.OnDuplicateError),
		"how --file handles a tuple that already exists: error|ignore (requires OpenFGA >= 1.10 for ignore)")
	registerBulkThroughputFlags(cmd, &bulk, "written")
	return cmd
}

// registerBulkThroughputFlags adds the --file execution flags shared by write
// and delete. done is the past participle used in the help text.
func registerBulkThroughputFlags(cmd *cobra.Command, bulk *bulkOpts, done string) {
	cmd.Flags().StringVar(&bulk.failedFile, "failed-file", "",
		"write the tuples that could not be "+done+" to this path, in the same format as --file, ready to re-run")
	cmd.Flags().IntVar(&bulk.maxPerChunk, "max-tuples-per-write", maxTuplesPerWrite,
		"tuples per --file request")
	cmd.Flags().IntVar(&bulk.maxParallel, "max-parallel-requests", 1,
		"--file requests to keep in flight at once")
}

func (c *Command) deleteCmd() *cobra.Command {
	var (
		force             bool
		dryRun            bool
		file, fileFormat  string
		fUser, fRel, fObj string
		bulk              bulkOpts
	)
	cmd := &cobra.Command{
		Use:     "delete [user] [relation] [object]",
		Aliases: []string{"rm"},
		Short:   "Delete one relationship tuple, or many with --file",
		Example: `  ofga tuples delete user:anne viewer document:roadmap
  ofga tuples delete --user user:anne --relation viewer --object document:roadmap
  ofga tuples delete --file tuples.json
  ofga tuples delete --file tuples.csv`,
		Long: "Delete one relationship tuple, or many with --file.\n\n" + bulkFileHelp,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				format, err := resolveBulkFormat(file, fileFormat)
				if err != nil {
					return err
				}
				bulk.format = format
				if err := bulk.validate(true); err != nil {
					return err
				}
				keys, err := bulkTuples(cmd, file, format, args, fUser, fRel, fObj, true)
				if err != nil {
					return err
				}
				if dryRun {
					if c.cli.JSON || c.cli.YAML {
						return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{"dry_run": true, "would_delete": len(keys)})
					}
					if output.Plain {
						return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"dry_run", "true"}, {"would_delete", fmt.Sprint(len(keys))}})
					}
					output.Infof(cmd.ErrOrStderr(), "would delete %d tuple(s)", len(keys))
					return nil
				}
				if err := prompt.Confirm(cmd,
					fmt.Sprintf("delete %d tuple(s)", len(keys)), force); err != nil {
					return err
				}
				cl, _, err := c.cli.ClientWithStore()
				if err != nil {
					return err
				}
				return runBulk(cmd, c.cli, cl, keys, true, bulk)
			}
			user, relation, object, err := fga.Triple(args, fUser, fRel, fObj)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			key, err := fga.ParseTuple(user, relation, object)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if dryRun {
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{"dry_run": true, "would_delete": 1})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"dry_run", "true"}, {"would_delete", "1"}})
				}
				output.Infof(cmd.ErrOrStderr(), "would delete %s", style.Bold.Render(fga.FormatTuple(key)))
				return nil
			}
			if err := prompt.Confirm(cmd,
				fmt.Sprintf("delete tuple %s", fga.FormatTuple(key)), force); err != nil {
				return err
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.WriteRequest{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{key}}}
			if err := cl.Tuples.Write(cmd.Context(), req); err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]int{"deleted": 1})
			}
			if output.Plain {
				return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"deleted", "1"}})
			}
			output.Successf(cmd.ErrOrStderr(), "deleted %s", style.Bold.Render(fga.FormatTuple(key)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the tuple that would be deleted without deleting it")
	cmd.Flags().StringVar(&file, "file", "", "file of tuples to delete in bulk ('-' for stdin)")
	cmd.Flags().StringVar(&fileFormat, "file-format", "", "format of --file: json|jsonl|yaml|csv (default: inferred from the extension; json for stdin)")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	cmd.Flags().StringVar(&bulk.onMissing, "on-missing", string(openfga.OnMissingError),
		"how --file handles a tuple that does not exist: error|ignore (requires OpenFGA >= 1.10 for ignore)")
	registerBulkThroughputFlags(cmd, &bulk, "deleted")
	return cmd
}

// readFilterUsage phrases the shared /read filter rule for a command line: the
// rule is the same one the playground's form applies, but a form names its
// fields and a command has to name its flags.
func readFilterUsage(f fga.ReadFilter) error {
	if err := fga.ValidateReadUser(f.User); err != nil {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--user %q: %w", f.User, err))
	}
	if err := fga.ValidateReadObject(f.Object); err != nil {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--object %q: %w", f.Object, err))
	}
	switch err := f.Validate(); {
	case errors.Is(err, fga.ErrReadFilterNeedsObject):
		return clierr.WithCode(clierr.CodeUsage, errors.New(
			"--object is required when filtering — pass a whole type (--object document:) or one object (--object document:roadmap)"))
	case errors.Is(err, fga.ErrReadFilterBareType):
		// Quote what they typed, as every sibling message does — a fixed example
		// reads as if the command misheard the flag.
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"--object %q is a whole type, so --user is required as well — or name one object (--object %s<id>)",
			f.Object, f.Object))
	case err != nil:
		return clierr.WithCode(clierr.CodeUsage, err)
	}
	return nil
}

func (c *Command) readCmd() *cobra.Command {
	var (
		user, relation, object string
		pageSize               int
		maxResults             int
		fConsistency           string
	)
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read relationship tuples (optionally filtered)",
		Example: `  ofga tuples read
  ofga tuples read --object document:roadmap
  ofga tuples read --max-results 100`,
		Long: "Read tuples from the store. Use --user, --relation and --object to filter. A filter needs " +
			"an object carrying a type — a whole type (document:) or one object (document:roadmap) — and a " +
			"bare type also needs a user; reading with no filter at all is fine. Object ids are matched " +
			"literally, so wildcards and usersets are rejected rather than quietly matching nothing. " +
			"By default all matching tuples are returned (the CLI auto-pages); --max-results (alias --limit) " +
			"caps the total returned and stops paging once reached. --page-size only tunes the per-request page.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxResults < 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--max-results must be non-negative"))
			}
			if pageSize < 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--page-size must be non-negative"))
			}
			// /read's tuple_key rule spans the three flags, and the server's 400
			// for it names a proto field rather than what to do about it. Catch it
			// here, with the same rule the playground's filter form applies — and
			// on the same trimmed values, since the server rejects whitespace too.
			filter := fga.NewReadFilter(user, relation, object)
			if err := readFilterUsage(filter); err != nil {
				return err
			}
			ropts, err := cli.ConsistencyOption(fConsistency)
			if err != nil {
				return err
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ReadRequest{PageSize: pageSize, TupleKey: filter.TupleKey()}
			output.Progressf(cmd.ErrOrStderr(), "fetching tuples…")
			var tuples []openfga.Tuple
			for t, err := range cl.Tuples.ReadAll(cmd.Context(), req, ropts...) {
				if err != nil {
					return err
				}
				tuples = append(tuples, t)
				if maxResults > 0 && len(tuples) >= maxResults {
					break
				}
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, tuples)
			}
			if len(tuples) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no tuples found")
				return nil
			}
			rows := make([][]string, 0, len(tuples))
			for _, t := range tuples {
				cond := ""
				if t.Key.Condition != nil {
					cond = output.SanitizeField(t.Key.Condition.Name)
				}
				rows = append(rows, []string{
					output.SanitizeField(t.Key.User),
					output.SanitizeField(t.Key.Relation),
					output.SanitizeField(t.Key.Object),
					cond,
					t.Timestamp.Format(time.RFC3339),
				})
			}
			if err := output.Table(cmd.OutOrStdout(), []string{"USER", "RELATION", "OBJECT", "CONDITION", "WRITTEN"}, rows); err != nil {
				return err
			}
			if err := output.HumanBlankLine(cmd.OutOrStdout()); err != nil {
				return err
			}
			output.Infof(cmd.ErrOrStderr(), "%d tuple(s)", len(tuples))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&user, "user", "", "filter by user")
	f.StringVar(&relation, "relation", "", "filter by relation")
	f.StringVar(&object, "object", "", "filter by object")
	f.IntVar(&pageSize, "page-size", 50, "per-request page size (0 = server default; not a total cap)")
	f.IntVar(&maxResults, "max-results", 0, "cap the total number of tuples returned (0 = unbounded)")
	f.IntVar(&maxResults, "limit", 0, "alias for --max-results")
	cli.RegisterConsistencyFlag(f, &fConsistency)
	return cmd
}

func (c *Command) changesCmd() *cobra.Command {
	var (
		typ        string
		startTime  string
		pageSize   int
		maxResults int
	)
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Show the tuple changelog (writes and deletes)",
		Example: `  ofga tuples changes
  ofga tuples changes --type document
  ofga tuples changes --max-results 100`,
		Long: "Show tuple changes. By default all currently available changes are returned (the CLI auto-pages); " +
			"--max-results (alias --limit) caps the total returned. --page-size only tunes the per-request page.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxResults < 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--max-results must be non-negative"))
			}
			if pageSize < 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--page-size must be non-negative"))
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			opts := &openfga.ReadChangesOptions{Type: typ, StartTime: startTime, PageSize: pageSize}
			var changes []openfga.TupleChange
			for ch, err := range cl.Tuples.ChangesAll(cmd.Context(), opts) {
				if err != nil {
					return err
				}
				changes = append(changes, ch)
				if maxResults > 0 && len(changes) >= maxResults {
					break
				}
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, changes)
			}
			if len(changes) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no changes found")
				return nil
			}
			rows := make([][]string, 0, len(changes))
			for _, ch := range changes {
				// --plain gets the bare token; the styled table keeps the glyph.
				op := "write"
				if ch.Operation == "TUPLE_OPERATION_DELETE" {
					op = "delete"
				}
				if !output.Plain {
					op = style.Success.Render("＋ write")
					if ch.Operation == "TUPLE_OPERATION_DELETE" {
						op = style.Failure.Render("－ delete")
					}
				}
				rows = append(rows, []string{
					ch.Timestamp.Format(time.RFC3339),
					op,
					output.SanitizeField(fga.FormatTuple(ch.TupleKey)),
				})
			}
			if err := output.Table(cmd.OutOrStdout(), []string{"TIMESTAMP", "OP", "TUPLE"}, rows); err != nil {
				return err
			}
			if err := output.HumanBlankLine(cmd.OutOrStdout()); err != nil {
				return err
			}
			output.Infof(cmd.ErrOrStderr(), "%d change(s)", len(changes))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "filter changes by object type")
	f.StringVar(&startTime, "start-time", "", "only changes at/after this RFC3339 time")
	f.IntVar(&pageSize, "page-size", 50, "per-request page size (0 = server default; not a total cap)")
	f.IntVar(&maxResults, "max-results", 0, "cap the total number of changes returned (0 = unbounded)")
	f.IntVar(&maxResults, "limit", 0, "alias for --max-results")
	return cmd
}

// bulkTuples reads and validates the tuples for a bulk --file operation. The
// file (or stdin for "-") is decoded in the format resolved from --file-format
// or the file extension; see parseTupleFile for the shapes each format accepts.
// --file is mutually exclusive with positional args and the
// --user/--relation/--object flags. Unknown fields on any tuple entry are
// rejected rather than silently ignored. forDelete rejects a condition on any
// entry: OpenFGA matches a delete by user/relation/object only, so a condition
// on a delete input would otherwise be silently dropped.
func bulkTuples(cmd *cobra.Command, file string, format bulkFormat, args []string, fUser, fRel, fObj string, forDelete bool) ([]openfga.TupleKey, error) {
	if len(args) > 0 || fUser != "" || fRel != "" || fObj != "" {
		return nil, clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--file cannot be combined with positional args or --user/--relation/--object"))
	}
	var data []byte
	var err error
	if file == "-" {
		data, err = readlimit.All(cmd.InOrStdin(), readlimit.Document, "tuples from stdin")
	} else {
		data, err = readlimit.File(file, readlimit.Document, "tuples file")
	}
	if err != nil {
		return nil, err
	}
	raw, err := parseTupleFile(data, format, file)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("no tuples in %s", file))
	}
	keys := make([]openfga.TupleKey, 0, len(raw))
	for i, t := range raw {
		key, err := fga.ParseTuple(t.User, t.Relation, t.Object)
		if err != nil {
			return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("tuple %d: %w", i+1, err))
		}
		if t.Condition != nil {
			if forDelete {
				return nil, clierr.WithCode(clierr.CodeUsage,
					fmt.Errorf("tuple %d: delete does not support a condition (deletes match by user/relation/object only)", i+1))
			}
			name := strings.TrimSpace(t.Condition.Name)
			if name == "" {
				return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("tuple %d: condition present but its name is empty", i+1))
			}
			key.Condition = &openfga.RelationshipCondition{Name: name, Context: t.Condition.Context}
		}
		keys = append(keys, key)
	}
	return keys, nil
}
