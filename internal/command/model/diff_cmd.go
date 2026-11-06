package model

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/output"
)

// diffOutput is what `model diff` prints under --json/--yaml. In the cost
// entries a weight of -1 means unbounded (infinite).
type diffOutput struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Old           string                `json:"old"`
	New           string                `json:"new"`
	Breaking      []modeldiff.Entry     `json:"breaking"`
	Changed       []modeldiff.Entry     `json:"changed"`
	Added         []modeldiff.Entry     `json:"added"`
	Cost          []modeldiff.CostEntry `json:"cost"`
	CostSkipped   string                `json:"costSkipped,omitempty" jsonschema_description:"Why the cost comparison could not be computed."`
	Summary       diffSummary           `json:"summary"`
}

// diffSummary counts the differences and gives the overall verdict.
type diffSummary struct {
	Breaking      int    `json:"breaking"`
	Changed       int    `json:"changed"`
	Added         int    `json:"added"`
	CostIncreased int    `json:"costIncreased"`
	CostDecreased int    `json:"costDecreased"`
	Verdict       string `json:"verdict" jsonschema:"enum=breaking,enum=cost_increased,enum=ok" jsonschema_description:"breaking when any breaking change exists, else cost_increased when any relation got more expensive, else ok."`
}

func newDiffOutput(old, new string, r modeldiff.Result) diffOutput {
	s := diffSummary{Breaking: len(r.Breaking), Changed: len(r.Changed), Added: len(r.Added), Verdict: "ok"}
	for _, c := range r.Cost {
		if c.Direction == modeldiff.CostIncreased {
			s.CostIncreased++
		} else {
			s.CostDecreased++
		}
	}
	switch {
	case s.Breaking > 0:
		s.Verdict = "breaking"
	case s.CostIncreased > 0:
		s.Verdict = "cost_increased"
	}
	return diffOutput{
		SchemaVersion: 1, Old: old, New: new,
		Breaking: r.Breaking, Changed: r.Changed, Added: r.Added, Cost: r.Cost,
		CostSkipped: r.CostSkipped, Summary: s,
	}
}

func (c *Command) diffCmd() *cobra.Command {
	var failOnCost bool
	cmd := &cobra.Command{
		Use:     "diff <old> [new]",
		Aliases: []string{"compare"},
		Short:   "Compare two authorization models and report breaking and cost changes",
		Example: `  ofga model diff model.fga
  ofga model diff old.fga new.fga
  ofga model diff store model.fga --fail-on-cost
  ofga model diff 01H… store`,
		Long: "Compare two authorization models and classify what changed.\n\n" +
			"Each argument is a model file (DSL or JSON), '-' for stdin, 'store' for the active store's latest model, or a model ID in the active store. " +
			"With one argument the old side is the store's latest model, so `ofga model diff model.fga` shows what would change if you wrote that file.\n\n" +
			"Breaking changes can make previously valid tuples or checks stop working: removed types, relations, directly related user types and conditions, or condition parameters that were removed or changed type. " +
			"Changed lists differences that alter behavior without removing anything, such as a rewritten relation definition or a changed condition expression. " +
			"Added lists new types, relations, user types and conditions.\n\n" +
			"Cost compares the per-user-type weights of each relation (see `model optimize`); an increase means checks on that relation can need more work. A weight of -1 in --json/--yaml output means unbounded; human output renders it inf.\n\n" +
			"Exit codes: 0 no breaking changes · 1 error · 2 usage (including an invalid model) · 3 a breaking change was found, or, with --fail-on-cost, a cost increase.",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			oldArg, newArg, err := resolveDiffArgs(args)
			if err != nil {
				return err
			}
			oldSide, err := c.loadDiffSide(cmd.Context(), cmd.InOrStdin(), "old", oldArg)
			if err != nil {
				return err
			}
			newSide, err := c.loadDiffSide(cmd.Context(), cmd.InOrStdin(), "new", newArg)
			if err != nil {
				return err
			}

			res := modeldiff.Diff(oldSide.Model, newSide.Model)
			out := newDiffOutput(oldSide.Label, newSide.Label, res)
			structured := c.cli.JSON || c.cli.YAML

			if res.CostSkipped != "" {
				warnKeepPlain(cmd.ErrOrStderr(), "cost comparison skipped: %s", res.CostSkipped)
			}
			if structured {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, out); err != nil {
					return err
				}
			} else {
				if err := renderDiff(cmd.OutOrStdout(), out); err != nil {
					return err
				}
				if res.HasCostIncrease() && !failOnCost {
					warnKeepPlain(cmd.ErrOrStderr(), "cost increased for %d relation(s); pass --fail-on-cost to fail on this", out.Summary.CostIncreased)
				}
			}

			if len(res.Breaking) > 0 || (failOnCost && res.HasCostIncrease()) {
				return clierr.Silent(clierr.CodeTestFailed, diffSummaryText(out.Summary))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&failOnCost, "fail-on-cost", false, "exit 3 when any relation got more expensive, not only on breaking changes")
	return cmd
}
