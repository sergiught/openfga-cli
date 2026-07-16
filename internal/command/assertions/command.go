// Package assertions implements `ofga assertions`: read and write the
// assertion test-suite attached to an authorization model, and run it.
package assertions

import (
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

// Command is the `assertions` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the assertions command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:     "assertions",
		Aliases: []string{"assert", "assertion"},
		RunE:    cli.GroupRunE,
		Short:   "Read, write and run a model's assertion test-suite",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands wires the assertions sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(c.readCmd(), c.writeCmd(), c.testCmd())
}

// resolveModelID returns the explicit id or the latest model's id.
func (c *Command) resolveModelID(cmd *cobra.Command, cl *openfga.Client, storeID, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	m, err := cl.AuthorizationModels.ReadLatest(cmd.Context(), openfga.WithStore(storeID))
	if err != nil {
		return "", fmt.Errorf("resolve latest model: %w", err)
	}
	return m.ID, nil
}

func (c *Command) readCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "read [model-id]",
		Short:   "Read the assertions for a model (default: latest)",
		Example: "  ofga assertions read",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, r, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			explicit := r.ModelID
			if len(args) == 1 {
				explicit = args[0]
			}
			modelID, err := c.resolveModelID(cmd, cl, r.StoreID, explicit)
			if err != nil {
				return err
			}
			res, err := cl.Assertions.Read(cmd.Context(), modelID, openfga.WithStore(r.StoreID))
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res)
			}
			if len(res.Assertions) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no assertions defined for model %s", modelID)
				return nil
			}
			rows := make([][]string, 0, len(res.Assertions))
			for _, a := range res.Assertions {
				exp := style.Success.Render("allow")
				if !a.Expectation {
					exp = style.Failure.Render("deny")
				}
				rows = append(rows, []string{
					output.SanitizeField(a.TupleKey.User),
					output.SanitizeField(a.TupleKey.Relation),
					output.SanitizeField(a.TupleKey.Object),
					exp,
				})
			}
			return output.Table(cmd.OutOrStdout(), []string{"USER", "RELATION", "OBJECT", "EXPECT"}, rows)
		},
	}
}

func (c *Command) writeCmd() *cobra.Command {
	var (
		file   string
		dryRun bool
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "write --file <assertions.json>",
		Short: "Replace the assertions for a model from a JSON file",
		Long:  "Replace a model's assertions (the active --model-id, or the latest). The file is a JSON array of assertions or an object {\"assertions\": [...]}, each: {\"tuple_key\":{\"user\",\"relation\",\"object\"},\"expectation\":true}.",
		Example: `  ofga assertions write --file assertions.json
  ofga assertions write --model-id 01H… --file assertions.json
  cat assertions.json | ofga assertions write --file -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := readFileOrStdin(file, cmd)
			if err != nil {
				return err
			}
			assertionsList, err := parseAssertions(data)
			if err != nil {
				return err
			}
			if dryRun {
				if c.cli.JSON || c.cli.YAML {
					return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{"dry_run": true, "would_write": len(assertionsList)})
				}
				if output.Plain {
					return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"dry_run", "true"}, {"would_write", fmt.Sprint(len(assertionsList))}})
				}
				output.Infof(cmd.ErrOrStderr(), "would write %d assertion(s)", len(assertionsList))
				return nil
			}
			cl, r, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			id, err := c.resolveModelID(cmd, cl, r.StoreID, r.ModelID)
			if err != nil {
				return err
			}
			if err := prompt.Confirm(cmd,
				fmt.Sprintf("replace all assertions for model %s with %d assertion(s)?", id, len(assertionsList)),
				force); err != nil {
				return err
			}
			req := &openfga.WriteAssertionsRequest{Assertions: assertionsList}
			if err := cl.Assertions.Write(cmd.Context(), id, req, openfga.WithStore(r.StoreID)); err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]int{"written": len(assertionsList)})
			}
			if output.Plain {
				return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"written", fmt.Sprint(len(assertionsList))}})
			}
			output.Successf(cmd.ErrOrStderr(), "wrote %d assertion(s) to model %s", len(assertionsList), id)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "assertions JSON file ('-' for stdin)")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "validate the file and show what would be written without writing it")
	cmd.Flags().BoolVar(&force, "force", false, "replace assertions without prompting")
	return cmd
}

func (c *Command) testCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "test [model-id]",
		Short:   "Run the model's assertions and report pass/fail",
		Example: "  ofga assertions test",
		Long:    "Read the stored assertions for a model and verify each one with a live Check, comparing the result to the expectation.",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, r, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			explicit := r.ModelID
			if len(args) == 1 {
				explicit = args[0]
			}
			modelID, err := c.resolveModelID(cmd, cl, r.StoreID, explicit)
			if err != nil {
				return err
			}
			res, err := cl.Assertions.Read(cmd.Context(), modelID, openfga.WithStore(r.StoreID))
			if err != nil {
				return err
			}
			if len(res.Assertions) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no assertions to run for model %s", modelID)
				return nil
			}
			output.Progressf(cmd.ErrOrStderr(), "testing %d assertion(s)…", len(res.Assertions))

			type result struct {
				Assertion string `json:"assertion"`
				Expected  bool   `json:"expected"`
				Got       bool   `json:"got"`
				Pass      bool   `json:"pass"`
			}
			var results []result
			passed := 0
			for _, a := range res.Assertions {
				var ctxTuples *openfga.ContextualTupleKeys
				if len(a.ContextualTuples) > 0 {
					ctxTuples = &openfga.ContextualTupleKeys{TupleKeys: a.ContextualTuples}
				}
				chk := &openfga.CheckRequest{
					TupleKey:         a.TupleKey,
					ContextualTuples: ctxTuples,
					Context:          a.Context,
				}
				cr, err := cl.Relationships.Check(cmd.Context(), chk,
					openfga.WithStore(r.StoreID), openfga.WithAuthorizationModel(modelID))
				if err != nil {
					return fmt.Errorf("check %s: %w", fga.FormatTuple(toTupleKey(a.TupleKey)), err)
				}
				pass := cr.Allowed == a.Expectation
				if pass {
					passed++
				}
				results = append(results, result{
					Assertion: fmt.Sprintf("%s %s %s", a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object),
					Expected:  a.Expectation, Got: cr.Allowed, Pass: pass,
				})
			}

			var assertionErr error
			if passed != len(results) {
				assertionErr = clierr.WithCode(clierr.CodeTestFailed,
					fmt.Errorf("%d assertion(s) failed", len(results)-passed))
			}
			outputErr := func(err error) error {
				return preferAssertionFailure(err, assertionErr)
			}

			if c.cli.JSON || c.cli.YAML {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]any{
					"authorization_model_id": modelID, "passed": passed, "total": len(results), "results": results,
				}); err != nil {
					return outputErr(err)
				}
				return assertionErr
			}
			if output.Plain {
				for _, res := range results {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%t\t%s\t%t\t%t\n",
						res.Pass, output.PlainField(res.Assertion), res.Expected, res.Got); err != nil {
						return outputErr(err)
					}
				}

				return assertionErr
			}
			for _, res := range results {
				mark := style.Success.Render(style.IconCheck)
				if !res.Pass {
					mark = style.Failure.Render(style.IconCross)
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", mark,
					style.Value.Render(output.SanitizeField(res.Assertion)),
					style.Faint.Render(fmt.Sprintf("(expected %v, got %v)", res.Expected, res.Got))); err != nil {
					return outputErr(err)
				}
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return outputErr(err)
			}
			summary := fmt.Sprintf("%d/%d passed", passed, len(results))
			if passed == len(results) {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), style.Success.Render(style.IconCheck+" "+summary))
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), style.Failure.Render(style.IconCross+" "+summary)); err != nil {
				return outputErr(err)
			}
			return assertionErr
		},
	}
}

// --- helpers ---

func preferAssertionFailure(outputErr, assertionErr error) error {
	if outputErr != nil && assertionErr != nil {
		return assertionErr
	}
	return outputErr
}

func toTupleKey(k openfga.CheckRequestTupleKey) openfga.TupleKey {
	return openfga.TupleKey{User: k.User, Relation: k.Relation, Object: k.Object}
}

func readFileOrStdin(path string, cmd *cobra.Command) ([]byte, error) {
	if path == "-" {
		return readlimit.All(cmd.InOrStdin(), readlimit.Document, "assertions from stdin")
	}
	return readlimit.File(path, readlimit.Document, "assertions file")
}

// parseAssertions accepts either a bare array or a {"assertions":[...]} object.
func parseAssertions(data []byte) ([]openfga.Assertion, error) {
	var wrapper struct {
		Assertions []openfga.Assertion `json:"assertions"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Assertions != nil {
		return wrapper.Assertions, nil
	}
	var list []openfga.Assertion
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("parse assertions JSON: %w", err))
	}
	return list, nil
}
