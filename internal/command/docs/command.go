// Package docs implements `ofga docs`: search and read the OpenFGA
// documentation on openfga.dev from the terminal.
package docs

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/docsearch"
	"github.com/sergiught/openfga-cli/internal/output"
)

// Command is the `docs` command group.
type Command struct {
	cli       *cli.CLI
	cmd       *cobra.Command
	newClient func() *docsearch.Client
}

// New builds the docs command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.newClient = func() *docsearch.Client { return docsearch.New(c.cli.RequestTimeout) }
	c.cmd = &cobra.Command{
		Use:   "docs",
		RunE:  cli.GroupRunE,
		Short: "Search and read the OpenFGA docs",
		Long:  "Search and read the OpenFGA documentation without leaving the terminal. Queries are sent to openfga.dev.",
	}
	c.cmd.AddCommand(c.searchCmd(), c.showCmd())
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return clierr.WithCode(clierr.CodeUsage, err)
		}
		return nil
	}
}

func (c *Command) searchCmd() *cobra.Command {
	var maxResults int
	cmd := &cobra.Command{
		Use:   "search <query...>",
		Short: "Search the OpenFGA docs",
		Long:  "Search the OpenFGA documentation. Queries are sent to openfga.dev.",
		Example: `  ofga docs search contextual tuples
  ofga docs search "conditions" --max-results 3
  ofga docs search modeling roles --json`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxResults < 1 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--max-results must be at least 1"))
			}
			query := strings.Join(args, " ")
			results, err := c.newClient().Search(cmd.Context(), query)
			if err != nil {
				return err
			}
			if len(results) > maxResults {
				results = results[:maxResults]
			}
			if c.cli.JSON || c.cli.YAML {
				if results == nil {
					results = []docsearch.Result{}
				}
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, results)
			}
			if len(results) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no results for %q", query)
				return nil
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{output.SanitizeField(r.Title), output.SanitizeField(r.URL)})
			}
			return output.Table(cmd.OutOrStdout(), []string{"TITLE", "URL"}, rows)
		},
	}
	cmd.Flags().IntVar(&maxResults, "max-results", 5, "maximum number of results")
	return cmd
}

func (c *Command) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <page|url>",
		Short: "Print a docs page as markdown",
		Long: `Print an OpenFGA docs page as markdown. Accepts a docs path
(such as /docs/modeling/conditions) or a full https://openfga.dev/docs/ URL.
Queries are sent to openfga.dev.`,
		Example: `  ofga docs show /docs/modeling/conditions
  ofga docs show https://openfga.dev/docs/interacting/relationship-queries --json`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			page, err := c.newClient().Page(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, page)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), page.Markdown)
			return err
		},
	}
}
