// Package tuple implements `ofga tuples`: write, delete, read relationship
// tuples and follow the changelog.
package tuple

import (
	"context"
	"encoding/json"
	"fmt"

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
		dryRun            bool
		file              string
		fUser, fRel, fObj string
	)
	cmd := &cobra.Command{
		Use:     "write [user] [relation] [object]",
		Aliases: []string{"add", "create"},
		Short:   "Write one relationship tuple, or many with --file",
		Example: `  ofga tuples write user:anne viewer document:roadmap
  ofga tuples write --user user:anne --relation viewer --object document:roadmap
  ofga tuples write --file tuples.json
  cat tuples.json | ofga tuples write --file -`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				keys, err := bulkTuples(cmd, file, args, fUser, fRel, fObj)
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
				written, err := writeInBatches(cmd.Context(), cl, keys, false)
				if err != nil {
					_ = emitBatchResult(cmd, c.cli, "written", written, len(keys), false)
					return err
				}
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]int{"written": len(keys)})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"written", fmt.Sprint(len(keys))}})
				}
				output.Successf(cmd.ErrOrStderr(), "wrote %d tuple(s)", len(keys))
				return nil
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
	cmd.Flags().StringVar(&file, "file", "", "JSON file of tuples to write in bulk ('-' for stdin)")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	return cmd
}

func (c *Command) deleteCmd() *cobra.Command {
	var (
		force             bool
		dryRun            bool
		file              string
		fUser, fRel, fObj string
	)
	cmd := &cobra.Command{
		Use:     "delete [user] [relation] [object]",
		Aliases: []string{"rm"},
		Short:   "Delete one relationship tuple, or many with --file",
		Example: `  ofga tuples delete user:anne viewer document:roadmap
  ofga tuples delete --user user:anne --relation viewer --object document:roadmap
  ofga tuples delete --file tuples.json`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				keys, err := bulkTuples(cmd, file, args, fUser, fRel, fObj)
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
				deleted, err := writeInBatches(cmd.Context(), cl, keys, true)
				if err != nil {
					_ = emitBatchResult(cmd, c.cli, "deleted", deleted, len(keys), false)
					return err
				}
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]int{"deleted": len(keys)})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"deleted", fmt.Sprint(len(keys))}})
				}
				output.Successf(cmd.ErrOrStderr(), "deleted %d tuple(s)", len(keys))
				return nil
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
	cmd.Flags().StringVar(&file, "file", "", "JSON file of tuples to delete in bulk ('-' for stdin)")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	return cmd
}

func (c *Command) readCmd() *cobra.Command {
	var (
		user, relation, object string
		pageSize               int
		maxResults             int
	)
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read relationship tuples (optionally filtered)",
		Example: `  ofga tuples read
  ofga tuples read --object document:roadmap
  ofga tuples read --max-results 100`,
		Long: "Read tuples from the store. Use --user, --relation and --object to filter; all are optional. " +
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
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ReadRequest{PageSize: pageSize}
			if user != "" || relation != "" || object != "" {
				req.TupleKey = &openfga.ReadRequestTupleKey{User: user, Relation: relation, Object: object}
			}
			output.Progressf(cmd.ErrOrStderr(), "fetching tuples…")
			var tuples []openfga.Tuple
			for t, err := range cl.Tuples.ReadAll(cmd.Context(), req) {
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
					t.Timestamp.Format("2006-01-02 15:04"),
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
				op := style.Success.Render("＋ write")
				if ch.Operation == "TUPLE_OPERATION_DELETE" {
					op = style.Failure.Render("－ delete")
				}
				rows = append(rows, []string{
					ch.Timestamp.Format("2006-01-02 15:04:05"),
					op,
					output.SanitizeField(fga.FormatTuple(ch.TupleKey)),
				})
			}
			return output.Table(cmd.OutOrStdout(), []string{"TIMESTAMP", "OP", "TUPLE"}, rows)
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

// tupleInput is one relationship tuple as it appears in a bulk --file: the
// canonical user/relation/object triple.
type tupleInput struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// bulkTuples reads and validates the tuples for a bulk --file operation. The
// file (or stdin for "-") is a JSON array of {user,relation,object} objects, or
// an object {"tuples":[...]}. --file is mutually exclusive with positional args
// and the --user/--relation/--object flags.
func bulkTuples(cmd *cobra.Command, file string, args []string, fUser, fRel, fObj string) ([]openfga.TupleKey, error) {
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
	var wrapper struct {
		Tuples []tupleInput `json:"tuples"`
	}
	var raw []tupleInput
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Tuples != nil {
		raw = wrapper.Tuples
	} else if err := json.Unmarshal(data, &raw); err != nil {
		return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("parse tuples file: %w", err))
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
		keys = append(keys, key)
	}
	return keys, nil
}

// writeInBatches writes (or deletes, when del is true) keys in chunks that stay
// under OpenFGA's per-request limit.
func writeInBatches(ctx context.Context, cl *openfga.Client, keys []openfga.TupleKey, del bool) (int, error) {
	completed := 0
	for i := 0; i < len(keys); i += maxTuplesPerWrite {
		end := min(i+maxTuplesPerWrite, len(keys))
		chunk := keys[i:end]
		req := &openfga.WriteRequest{}
		if del {
			req.Deletes = &openfga.WriteRequestTuples{TupleKeys: chunk}
		} else {
			req.Writes = &openfga.WriteRequestTuples{TupleKeys: chunk}
		}
		if err := cl.Tuples.Write(ctx, req); err != nil {
			return completed, fmt.Errorf("tuples %d-%d failed after %d of %d tuple(s) were committed: %w",
				i+1, end, completed, len(keys), err)
		}
		completed = end
	}
	return completed, nil
}

func emitBatchResult(cmd *cobra.Command, c *cli.CLI, field string, completed, total int, complete bool) error {
	if c.JSON || c.YAML {
		return output.Emit(cmd.OutOrStdout(), c.YAML, map[string]any{
			field: completed, "total": total, "complete": complete,
		})
	}
	if output.Plain {
		return output.KeyValues(cmd.OutOrStdout(), [][2]string{
			{field, fmt.Sprint(completed)},
			{"total", fmt.Sprint(total)},
			{"complete", fmt.Sprint(complete)},
		})
	}
	return nil
}
