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
	return &cobra.Command{
		Use:     "write <user> <relation> <object>",
		Aliases: []string{"add"},
		Short:   "Write a relationship tuple",
		Example: "  ofga tuples write user:anne viewer document:roadmap",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := fga.ParseTuple(args[0], args[1], args[2])
			if err != nil {
				return err
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
}

func (c *Command) deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <user> <relation> <object>",
		Aliases: []string{"rm"},
		Short:   "Delete a relationship tuple",
		Example: "  ofga tuples delete user:anne viewer document:roadmap",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := fga.ParseTuple(args[0], args[1], args[2])
			if err != nil {
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
}

func (c *Command) readCmd() *cobra.Command {
	var (
		user, relation, object string
		pageSize               int
	)
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read relationship tuples (optionally filtered)",
		Long:  "Read tuples from the store. Use --user, --relation and --object to filter; all are optional.",
		Args:  cobra.NoArgs,
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
		Args:  cobra.NoArgs,
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
