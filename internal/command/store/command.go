// Package store implements `ofga stores`: create, list, inspect and delete
// OpenFGA stores.
package store

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/output"
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
		Short:   "Create, list, inspect and delete stores",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

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
	var use bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			output.Successf(cmd.OutOrStdout(), "created store %s", style.Bold.Render(st.Name))
			output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"id", st.ID},
				{"name", st.Name},
				{"created_at", st.CreatedAt.Format("2006-01-02 15:04:05")},
			})
			if use {
				output.Infof(cmd.OutOrStdout(), "set as the active profile's store")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&use, "use", false, "save the new store ID to the active profile")
	return cmd
}

func (c *Command) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all stores",
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
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), stores)
			}
			if len(stores) == 0 {
				output.Infof(cmd.OutOrStdout(), "no stores found")
				return nil
			}
			rows := make([][]string, 0, len(stores))
			for _, st := range stores {
				rows = append(rows, []string{st.ID, st.Name, st.CreatedAt.Format("2006-01-02 15:04")})
			}
			output.Table(cmd.OutOrStdout(), []string{"ID", "NAME", "CREATED"}, rows)
			fmt.Fprintln(cmd.OutOrStdout())
			output.Infof(cmd.OutOrStdout(), "%d store(s)", len(stores))
			return nil
		},
	}
}

func (c *Command) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <store-id>",
		Short: "Show details of a store",
		Args:  cobra.ExactArgs(1),
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
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <store-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a store",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete store %s without --yes (this cannot be undone)", args[0])
			}
			cl, err := c.cli.Client()
			if err != nil {
				return err
			}
			if err := cl.Stores.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "deleted store %s", style.Bold.Render(args[0]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	return cmd
}
