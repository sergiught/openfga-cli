package model

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// testSchemaCmd prints the JSON Schema for the model-test workspace format, so
// it can be saved and pointed at from an editor's `$schema` binding.
func (c *Command) testSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for the model-test workspace format",
		Long: "Print the JSON Schema that describes the ofga.yaml manifest and *.test.yaml files. " +
			"There is no hosted URL for it, so save the output and point your editor at it, e.g.:\n\n" +
			"  ofga model test schema > workspace.schema.json\n\n" +
			"then add a modeline to a workspace file:\n\n" +
			"  # yaml-language-server: $schema=./workspace.schema.json",
		Example: `  ofga model test schema
  ofga model test schema > workspace.schema.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), string(modeltest.WorkspaceSchema()))
			return err
		},
	}
}
