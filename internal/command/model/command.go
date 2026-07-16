// Package model implements `ofga model`: write, list, inspect, and visualize
// authorization models (including a colored relation graph).
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/readlimit"
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
		RunE:    cli.GroupRunE,
		Short:   "Write, inspect and visualize authorization models",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// completeModelIDs suggests authorization model IDs for the resolved store.
func (c *Command) completeModelIDs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cl, _, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
	defer cancel()
	var out []string
	for m, err := range cl.AuthorizationModels.All(ctx, nil) {
		if err != nil {
			break
		}
		out = append(out, m.ID)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

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
		Use:     "write --file <model.json>",
		Aliases: []string{"create"},
		Short:   "Write a new authorization model from a JSON file",
		Example: `  ofga model write --file model.json
  cat model.json | ofga model write --file -`,
		Long: "Write a new authorization model. The file must be the model JSON with schema_version and type_definitions (the format produced by `fga model transform` or the OpenFGA API).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var data []byte
			var err error
			if file == "-" {
				data, err = readlimit.All(cmd.InOrStdin(), readlimit.Document, "model from stdin")
			} else {
				data, err = readlimit.File(file, readlimit.Document, "model file")
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
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{
						"dry_run": true, "schema_version": req.SchemaVersion, "type_definitions": len(req.TypeDefinitions),
					})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{
						{"dry_run", "true"},
						{"schema_version", req.SchemaVersion},
						{"type_definitions", fmt.Sprint(len(req.TypeDefinitions))},
					})
				}
				output.Infof(cmd.ErrOrStderr(), "would write authorization model (schema %s, %d type definitions)",
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
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res)
			}
			output.Successf(cmd.ErrOrStderr(), "wrote authorization model")
			return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"model_id", res.AuthorizationModelID}})
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "path to the model JSON file")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "validate the file and show what would be written without writing it")
	return cmd
}

func (c *Command) listCmd() *cobra.Command {
	var maxResults int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List authorization models in the store",
		Example: "  ofga model list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxResults < 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--max-results must be non-negative"))
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			output.Progressf(cmd.ErrOrStderr(), "fetching authorization models…")
			var models []openfga.AuthorizationModel
			for m, err := range cl.AuthorizationModels.All(cmd.Context(), nil) {
				if err != nil {
					return err
				}
				models = append(models, m)
				if maxResults > 0 && len(models) >= maxResults {
					break
				}
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, models)
			}
			if len(models) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no models found")
				return nil
			}
			rows := make([][]string, 0, len(models))
			for i, m := range models {
				marker := ""
				if i == 0 {
					marker = style.Success.Render("latest")
				}
				rows = append(rows, []string{
					output.SanitizeField(m.ID),
					output.SanitizeField(m.SchemaVersion),
					fmt.Sprintf("%d", len(m.TypeDefinitions)),
					marker,
				})
			}
			return output.Table(cmd.OutOrStdout(), []string{"MODEL ID", "SCHEMA", "TYPES", ""}, rows)
		},
	}
	cmd.Flags().IntVar(&maxResults, "max-results", 0, "cap the total number of models returned (0 = unbounded)")
	cmd.Flags().IntVar(&maxResults, "limit", 0, "alias for --max-results")
	return cmd
}

func (c *Command) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "get <model-id>",
		ValidArgsFunction: c.completeModelIDs,
		Short:             "Show an authorization model as JSON",
		Example:           "  ofga model get 01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			m, err := cl.AuthorizationModels.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.Emit(cmd.OutOrStdout(), c.cli.YAML, m)
		},
	}
}

func (c *Command) latestCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "latest",
		Short:   "Show the most recent authorization model",
		Example: "  ofga model latest",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			m, err := cl.AuthorizationModels.ReadLatest(cmd.Context())
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, m)
			}
			return output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"model_id", m.ID},
				{"schema", m.SchemaVersion},
				{"types", fmt.Sprintf("%d", len(m.TypeDefinitions))},
			})
		},
	}
}

func (c *Command) graphCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "graph [model-id]",
		Short:   "Render the model as a colored relation graph",
		Example: "  ofga model graph",
		Long:    "Render an authorization model as a colored tree showing, for each type and relation, the directly-assignable types, implied relations, and inherited (tuple-to-userset) paths. With no argument, the latest model is used.",
		Args:    cobra.MaximumNArgs(1),
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
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, g)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(),
				style.Title.Render("Authorization Model "+output.SanitizeField(m.ID))); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), g.Render())
			return err
		},
	}
}
