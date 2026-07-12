// Package model implements `ofga model`: write, list, inspect, and visualize
// authorization models (including a colored relation graph).
package model

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

// Command is the `model` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the model command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:     "model",
		Aliases: []string{"models"},
		Short:   "Write, inspect and visualize authorization models",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands wires the model sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		c.writeCmd(),
		c.listCmd(),
		c.getCmd(),
		c.latestCmd(),
		c.graphCmd(),
	)
}

func (c *Command) writeCmd() *cobra.Command {
	var (
		file   string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "write --file <model.json>",
		Short: "Write a new authorization model from a JSON file",
		Long:  "Write a new authorization model. The file must be the model JSON with schema_version and type_definitions (the format produced by `fga model transform` or the OpenFGA API).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("--file is required (use '-' to read the model JSON from stdin)")
			}
			var data []byte
			var err error
			if file == "-" {
				data, err = io.ReadAll(cmd.InOrStdin())
			} else {
				data, err = os.ReadFile(file)
			}
			if err != nil {
				return fmt.Errorf("read model: %w", err)
			}
			var req openfga.WriteAuthorizationModelRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return fmt.Errorf("parse model JSON: %w", err)
			}
			if req.SchemaVersion == "" {
				req.SchemaVersion = "1.1"
			}
			if dryRun {
				output.Infof(cmd.OutOrStdout(), "would write authorization model (schema %s, %d type definitions)",
					req.SchemaVersion, len(req.TypeDefinitions))
				return nil
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			res, err := cl.AuthorizationModels.Write(cmd.Context(), &req)
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res)
			}
			output.Successf(cmd.OutOrStdout(), "wrote authorization model")
			output.KeyValues(cmd.OutOrStdout(), [][2]string{{"model_id", res.AuthorizationModelID}})
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "path to the model JSON file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate the file and show what would be written without writing it")
	return cmd
}

func (c *Command) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List authorization models in the store",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			var models []openfga.AuthorizationModel
			for m, err := range cl.AuthorizationModels.All(cmd.Context(), nil) {
				if err != nil {
					return err
				}
				models = append(models, m)
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), models)
			}
			if len(models) == 0 {
				output.Infof(cmd.OutOrStdout(), "no models found")
				return nil
			}
			rows := make([][]string, 0, len(models))
			for i, m := range models {
				marker := ""
				if i == 0 {
					marker = style.Success.Render("latest")
				}
				rows = append(rows, []string{m.ID, m.SchemaVersion, fmt.Sprintf("%d", len(m.TypeDefinitions)), marker})
			}
			output.Table(cmd.OutOrStdout(), []string{"MODEL ID", "SCHEMA", "TYPES", ""}, rows)
			return nil
		},
	}
}

func (c *Command) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <model-id>",
		Short: "Show an authorization model as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			m, err := cl.AuthorizationModels.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.JSON(cmd.OutOrStdout(), m)
		},
	}
}

func (c *Command) latestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "latest",
		Short: "Show the most recent authorization model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			m, err := cl.AuthorizationModels.ReadLatest(cmd.Context())
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), m)
			}
			output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"model_id", m.ID},
				{"schema", m.SchemaVersion},
				{"types", fmt.Sprintf("%d", len(m.TypeDefinitions))},
			})
			return nil
		},
	}
}

func (c *Command) graphCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "graph [model-id]",
		Short: "Render the model as a colored relation graph",
		Long:  "Render an authorization model as a colored tree showing, for each type and relation, the directly-assignable types, implied relations, and inherited (tuple-to-userset) paths. With no argument, the latest model is used.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			var m *openfga.AuthorizationModel
			if len(args) == 1 {
				m, err = cl.AuthorizationModels.Get(cmd.Context(), args[0])
			} else {
				m, err = cl.AuthorizationModels.ReadLatest(cmd.Context())
			}
			if err != nil {
				return err
			}
			g := fga.ParseModel(m)
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), g)
			}
			fmt.Fprintln(cmd.OutOrStdout(), style.Title.Render("Authorization Model "+m.ID))
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), g.Render())
			return nil
		},
	}
}
