// Package assertions implements `ofga assertions`: read and write the
// assertion test-suite attached to an authorization model, and run it.
package assertions

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
		Use:   "read [model-id]",
		Short: "Read the assertions for a model (default: latest)",
		Args:  cobra.MaximumNArgs(1),
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
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res)
			}
			if len(res.Assertions) == 0 {
				output.Infof(cmd.OutOrStdout(), "no assertions defined for model %s", modelID)
				return nil
			}
			rows := make([][]string, 0, len(res.Assertions))
			for _, a := range res.Assertions {
				exp := style.Success.Render("allow")
				if !a.Expectation {
					exp = style.Failure.Render("deny")
				}
				rows = append(rows, []string{a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object, exp})
			}
			output.Table(cmd.OutOrStdout(), []string{"USER", "RELATION", "OBJECT", "EXPECT"}, rows)
			return nil
		},
	}
}

func (c *Command) writeCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "write --file <assertions.json>",
		Short: "Replace the assertions for a model from a JSON file",
		Long:  "Replace a model's assertions (the active --model, or the latest). The file is a JSON array of assertions or an object {\"assertions\": [...]}, each: {\"tuple_key\":{\"user\",\"relation\",\"object\"},\"expectation\":true}.",
		Example: `  ofga assertions write --file assertions.json
  ofga assertions write --model 01H… --file assertions.json
  cat assertions.json | ofga assertions write --file -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			data, err := readFileOrStdin(file, cmd)
			if err != nil {
				return err
			}
			assertionsList, err := parseAssertions(data)
			if err != nil {
				return err
			}
			cl, r, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			id, err := c.resolveModelID(cmd, cl, r.StoreID, r.ModelID)
			if err != nil {
				return err
			}
			req := &openfga.WriteAssertionsRequest{Assertions: assertionsList}
			if err := cl.Assertions.Write(cmd.Context(), id, req, openfga.WithStore(r.StoreID)); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "wrote %d assertion(s) to model %s", len(assertionsList), id)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "assertions JSON file ('-' for stdin)")
	return cmd
}

func (c *Command) testCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test [model-id]",
		Short: "Run the model's assertions and report pass/fail",
		Long:  "Read the stored assertions for a model and verify each one with a live Check, comparing the result to the expectation.",
		Args:  cobra.MaximumNArgs(1),
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
				output.Infof(cmd.OutOrStdout(), "no assertions to run for model %s", modelID)
				return nil
			}

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

			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]any{
					"model_id": modelID, "passed": passed, "total": len(results), "results": results,
				})
			}
			for _, res := range results {
				mark := style.Success.Render(style.IconCheck)
				if !res.Pass {
					mark = style.Failure.Render(style.IconCross)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", mark,
					style.Value.Render(res.Assertion),
					style.Faint.Render(fmt.Sprintf("(expected %v, got %v)", res.Expected, res.Got)))
			}
			fmt.Fprintln(cmd.OutOrStdout())
			summary := fmt.Sprintf("%d/%d passed", passed, len(results))
			if passed == len(results) {
				fmt.Fprintln(cmd.OutOrStdout(), style.Success.Render(style.IconCheck+" "+summary))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), style.Failure.Render(style.IconCross+" "+summary))
			return fmt.Errorf("%d assertion(s) failed", len(results)-passed)
		},
	}
}

// --- helpers ---

func toTupleKey(k openfga.CheckRequestTupleKey) openfga.TupleKey {
	return openfga.TupleKey{User: k.User, Relation: k.Relation, Object: k.Object}
}

func readFileOrStdin(path string, cmd *cobra.Command) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
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
		return nil, fmt.Errorf("parse assertions JSON: %w", err)
	}
	return list, nil
}
