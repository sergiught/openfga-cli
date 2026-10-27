package model

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/output"
)

// testSchemaCmd prints the JSON Schema for the model-test workspace format, so
// it can be saved and pointed at from an editor's `$schema` binding.
func (c *Command) testSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for the model-test workspace format",
		Long: "Print the JSON Schema that describes the ofga.yaml manifest and *.test.yaml files. " +
			"A hosted copy is available at:\n\n" +
			"  " + modeltest.WorkspaceSchemaURL + "\n\n" +
			"Use its #manifest fragment for ofga.yaml or #testFile for *.test.yaml.\n\n" +
			"To pin the schema shipped with your installed CLI, save it locally:\n\n" +
			"  ofga model test schema > workspace.schema.json\n\n" +
			"then add a modeline to a workspace file:\n\n" +
			"  # yaml-language-server: $schema=./workspace.schema.json#manifest",
		Example: `  ofga model test schema
  ofga model test schema > workspace.schema.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// -o jsonl and agent mode promise one compact line, so the
			// indented embedded copy only goes out as-is otherwise.
			if output.JSONL || output.Compact {
				return output.RawJSONLines(cmd.OutOrStdout(), modeltest.WorkspaceSchema())
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), string(modeltest.WorkspaceSchema()))
			return err
		},
	}
}
