// Package tuple implements `ofga tuples`: write, delete, read relationship
// tuples and follow the changelog.
package tuple

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/style"
)

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
		fUser, fRel, fObj string
	)
	cmd := &cobra.Command{
		Use:     "write [user] [relation] [object]",
		Aliases: []string{"add"},
		Short:   "Write a relationship tuple",
		Example: `  ofga tuples write user:anne viewer document:roadmap
  ofga tuples write --user user:anne --relation viewer --object document:roadmap`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, relation, object, err := fga.Triple(args, fUser, fRel, fObj)
			if err != nil {
				return err
			}
			key, err := fga.ParseTuple(user, relation, object)
			if err != nil {
				return err
			}
			if dryRun {
				output.Infof(cmd.OutOrStdout(), "would write %s", style.Bold.Render(fga.FormatTuple(key)))
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
			output.Successf(cmd.OutOrStdout(), "wrote %s", style.Bold.Render(fga.FormatTuple(key)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the tuple that would be written without writing it")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	return cmd
}

func (c *Command) deleteCmd() *cobra.Command {
	var (
		force             bool
		dryRun            bool
		fUser, fRel, fObj string
	)
	cmd := &cobra.Command{
		Use:     "delete [user] [relation] [object]",
		Aliases: []string{"rm"},
		Short:   "Delete a relationship tuple",
		Example: `  ofga tuples delete user:anne viewer document:roadmap
  ofga tuples delete --user user:anne --relation viewer --object document:roadmap`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, relation, object, err := fga.Triple(args, fUser, fRel, fObj)
			if err != nil {
				return err
			}
			key, err := fga.ParseTuple(user, relation, object)
			if err != nil {
				return err
			}
			if dryRun {
				output.Infof(cmd.OutOrStdout(), "would delete %s", style.Bold.Render(fga.FormatTuple(key)))
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
			output.Successf(cmd.OutOrStdout(), "deleted %s", style.Bold.Render(fga.FormatTuple(key)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the tuple that would be deleted without deleting it")
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	return cmd
}

func (c *Command) readCmd() *cobra.Command {
	var (
		user, relation, object string
		pageSize               int
	)
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read relationship tuples (optionally filtered)",
		Example: `  ofga tuples read
  ofga tuples read --object document:roadmap`,
		Long: "Read tuples from the store. Use --user, --relation and --object to filter; all are optional.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ReadRequest{PageSize: pageSize}
			if user != "" || relation != "" || object != "" {
				req.TupleKey = &openfga.ReadRequestTupleKey{User: user, Relation: relation, Object: object}
			}
			var tuples []openfga.Tuple
			for t, err := range cl.Tuples.ReadAll(cmd.Context(), req) {
				if err != nil {
					return err
				}
				tuples = append(tuples, t)
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), tuples)
			}
			if len(tuples) == 0 {
				output.Infof(cmd.OutOrStdout(), "no tuples found")
				return nil
			}
			rows := make([][]string, 0, len(tuples))
			for _, t := range tuples {
				cond := ""
				if t.Key.Condition != nil {
					cond = t.Key.Condition.Name
				}
				rows = append(rows, []string{t.Key.User, t.Key.Relation, t.Key.Object, cond, t.Timestamp.Format("2006-01-02 15:04")})
			}
			output.Table(cmd.OutOrStdout(), []string{"USER", "RELATION", "OBJECT", "CONDITION", "WRITTEN"}, rows)
			fmt.Fprintln(cmd.OutOrStdout())
			output.Infof(cmd.OutOrStdout(), "%d tuple(s)", len(tuples))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&user, "user", "", "filter by user")
	f.StringVar(&relation, "relation", "", "filter by relation")
	f.StringVar(&object, "object", "", "filter by object")
	f.IntVar(&pageSize, "page-size", 50, "page size")
	return cmd
}

func (c *Command) changesCmd() *cobra.Command {
	var (
		typ       string
		startTime string
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Show the tuple changelog (writes and deletes)",
		Example: `  ofga tuples changes
  ofga tuples changes --type document`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			opts := &openfga.ReadChangesOptions{Type: typ, StartTime: startTime}
			var changes []openfga.TupleChange
			for ch, err := range cl.Tuples.ChangesAll(cmd.Context(), opts) {
				if err != nil {
					return err
				}
				changes = append(changes, ch)
				if limit > 0 && len(changes) >= limit {
					break
				}
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), changes)
			}
			if len(changes) == 0 {
				output.Infof(cmd.OutOrStdout(), "no changes found")
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
					fga.FormatTuple(ch.TupleKey),
				})
			}
			output.Table(cmd.OutOrStdout(), []string{"TIMESTAMP", "OP", "TUPLE"}, rows)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "filter changes by object type")
	f.StringVar(&startTime, "start-time", "", "only changes at/after this RFC3339 time")
	f.IntVar(&limit, "limit", 100, "maximum number of changes to display (0 for all)")
	return cmd
}
