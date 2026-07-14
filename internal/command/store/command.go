// Package store implements `ofga stores`: create, list, inspect and delete
// OpenFGA stores.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/style"
)

// Command is the `store` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the store command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:     "stores",
		Aliases: []string{"store"},
		RunE:    cli.GroupRunE,
		Short:   "Create, list, inspect and delete stores",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// completeIDs suggests store IDs (with names) from the API for the first arg.
func (c *Command) completeIDs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cl, err := c.cli.Client()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
	defer cancel()
	var out []string
	for st, err := range cl.Stores.All(ctx, nil) {
		if err != nil {
			break
		}
		out = append(out, st.ID+"\t"+st.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// RegisterSubCommands wires the store sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		c.createCmd(),
		c.listCmd(),
		c.getCmd(),
		c.deleteCmd(),
	)
}

func (c *Command) createCmd() *cobra.Command {
	var (
		use    bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a new store",
		Example: "  ofga stores create my-store --use",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun {
				if c.cli.JSON {
					return output.JSON(cmd.OutOrStdout(), map[string]any{"dry_run": true, "would_create": args[0]})
				}
				output.Infof(cmd.ErrOrStderr(), "would create store %s", style.Bold.Render(args[0]))
				return nil
			}
			cl, err := c.cli.Client()
			if err != nil {
				return err
			}
			st, err := cl.Stores.Create(cmd.Context(), &openfga.CreateStoreRequest{Name: args[0]})
			if err != nil {
				return err
			}
			if use {
				name := c.cli.Config.Active
				if c.cli.Overrides.Profile != "" {
					name = c.cli.Overrides.Profile
				}
				if p, ok := c.cli.Config.Get(name); ok {
					p.StoreID = st.ID
					c.cli.Config.Set(name, p)
					if err := c.cli.SaveConfig(); err != nil {
						return err
					}
				}
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), st)
			}
			output.Successf(cmd.ErrOrStderr(), "created store %s", style.Bold.Render(st.Name))
			output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"id", st.ID},
				{"name", st.Name},
				{"created_at", st.CreatedAt.Format("2006-01-02 15:04:05")},
			})
			if use {
				output.Infof(cmd.ErrOrStderr(), "set as the active profile's store")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&use, "use", false, "save the new store ID to the active profile")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be created without creating it")
	return cmd
}

func (c *Command) listCmd() *cobra.Command {
	var maxResults int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all stores",
		Example: "  ofga stores list",
		Long:    "List stores. By default all stores are returned (the CLI auto-pages); --max-results caps the total returned and stops paging once reached.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := c.cli.Client()
			if err != nil {
				return err
			}
			var stores []openfga.Store
			for st, err := range cl.Stores.All(cmd.Context(), nil) {
				if err != nil {
					return err
				}
				stores = append(stores, st)
				if maxResults > 0 && len(stores) >= maxResults {
					break
				}
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), stores)
			}
			if len(stores) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no stores found")
				return nil
			}
			rows := make([][]string, 0, len(stores))
			for _, st := range stores {
				rows = append(rows, []string{st.ID, st.Name, st.CreatedAt.Format("2006-01-02 15:04")})
			}
			output.Table(cmd.OutOrStdout(), []string{"ID", "NAME", "CREATED"}, rows)
			fmt.Fprintln(cmd.OutOrStdout())
			output.Infof(cmd.ErrOrStderr(), "%d store(s)", len(stores))
			return nil
		},
	}
	cmd.Flags().IntVar(&maxResults, "max-results", 0, "cap the total number of stores returned (0 = unbounded)")
	return cmd
}

func (c *Command) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "get <store-id>",
		ValidArgsFunction: c.completeIDs,
		Short:             "Show details of a store",
		Example:           "  ofga stores get 01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := c.cli.Client()
			if err != nil {
				return err
			}
			st, err := cl.Stores.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), st)
			}
			output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"id", st.ID},
				{"name", st.Name},
				{"created_at", st.CreatedAt.Format("2006-01-02 15:04:05")},
				{"updated_at", st.UpdatedAt.Format("2006-01-02 15:04:05")},
			})
			return nil
		},
	}
}

func (c *Command) deleteCmd() *cobra.Command {
	var (
		force  bool
		yes    bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:               "delete <store-id>",
		Aliases:           []string{"rm"},
		ValidArgsFunction: c.completeIDs,
		Short:             "Delete a store",
		Example:           "  ofga stores delete 01ARZ3NDEKTSV4RRFFQ69G5FAV --force",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun {
				if c.cli.JSON {
					return output.JSON(cmd.OutOrStdout(), map[string]any{"dry_run": true, "would_delete": args[0]})
				}
				output.Infof(cmd.ErrOrStderr(), "would delete store %s", style.Bold.Render(args[0]))
				return nil
			}
			// Deleting a store destroys all of its models, tuples and
			// assertions, so require typing the store ID (or --force).
			if err := prompt.ConfirmName(cmd,
				fmt.Sprintf("delete store %s and all its data — this cannot be undone", args[0]),
				args[0], force || yes); err != nil {
				return err
			}
			cl, err := c.cli.Client()
			if err != nil {
				return err
			}
			if err := cl.Stores.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{"deleted": args[0]})
			}
			output.Successf(cmd.ErrOrStderr(), "deleted store %s", style.Bold.Render(args[0]))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be deleted without deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "deprecated: use --force")
	_ = cmd.Flags().MarkDeprecated("yes", "use --force")
	return cmd
}
