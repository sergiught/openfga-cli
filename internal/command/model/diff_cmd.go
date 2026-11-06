package model

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/modeltest"
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
	var report, reportFile string
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
			"Breaking changes can make previously valid tuples or checks stop working: removed types, relations, directly related user types and conditions, or condition parameters that were added, removed or changed type. " +
			"Changed lists differences whose effect on access is not analysed, such as a rewritten relation definition or a changed condition expression: they can grant or remove access, so review them. " +
			"Added lists new types, relations, user types and conditions.\n\n" +
			"Cost compares the per-user-type weights of each relation (see `model optimize`); an increase means checks on that relation can need more work. A weight of -1 in --json/--yaml output means unbounded; human output renders it inf.\n\n" +
			"Exit codes: 0 no breaking changes · 1 error · 2 usage (including an invalid model) · 3 a breaking change was found, or, with --fail-on-cost, a cost increase · 4 the server was unreachable.",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if report != "" && report != "github" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("invalid --report %q (want \"github\")", report))
			}
			if reportFile != "" && report == "" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report-file requires --report"))
			}
			if report != "" && reportFile == "" && (c.cli.JSON || c.cli.YAML) {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report without --report-file prints to stdout, which conflicts with -o json/yaml; pass --report-file"))
			}
			if f := cmd.Flags().Lookup("model-id"); f != nil && f.Changed {
				return clierr.WithCode(clierr.CodeUsage, errors.New("--model-id is not used by model diff; pass the model ID as an argument instead"))
			}
			oldArg, newArg, err := resolveDiffArgs(args)
			if err != nil {
				return err
			}
			// Load file and stdin sides first so a bad local file is reported
			// before any network round trip or "no store selected".
			var oldSide, newSide diffSide
			loadOld := func() (err error) {
				oldSide, err = c.loadDiffSide(cmd.Context(), cmd.InOrStdin(), "old", oldArg)
				return err
			}
			loadNew := func() (err error) {
				newSide, err = c.loadDiffSide(cmd.Context(), cmd.InOrStdin(), "new", newArg)
				return err
			}
			first, second := loadOld, loadNew
			if isStoreArg(oldArg) && !isStoreArg(newArg) {
				first, second = loadNew, loadOld
			}
			if err := first(); err != nil {
				return err
			}
			if err := second(); err != nil {
				return err
			}

			res := modeldiff.Diff(oldSide.Model, newSide.Model)
			out := newDiffOutput(oldSide.Label, newSide.Label, res)
			structured := c.cli.JSON || c.cli.YAML

			if res.CostSkipped != "" && !structured {
				warnKeepPlain(cmd.ErrOrStderr(), "cost comparison skipped: %s", res.CostSkipped)
			}
			if structured {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, out); err != nil {
					return err
				}
			} else {
				if err := renderDiff(cmd.OutOrStdout(), out, res); err != nil {
					return err
				}
				if res.HasCostIncrease() && !failOnCost {
					warnKeepPlain(cmd.ErrOrStderr(), "cost increased for %d relation(s); pass --fail-on-cost to fail on this", out.Summary.CostIncreased)
				}
			}

			if report != "" {
				w := cmd.OutOrStdout()
				var staged *atomicfile.File
				if reportFile != "" {
					f, err := createReportFile(reportFile)
					if err != nil {
						return err
					}
					defer f.Abort()
					staged, w = f, f
				}
				if err := writeDiffAnnotations(w, oldSide, newSide, res); err != nil {
					return err
				}
				if staged != nil {
					if err := staged.Commit(); err != nil {
						return err
					}
				}
			}

			if len(res.Breaking) > 0 || (failOnCost && res.HasCostIncrease()) {
				return clierr.Silent(clierr.CodeTestFailed, diffSummaryText(out.Summary))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&failOnCost, "fail-on-cost", false, "exit 3 when any relation got more expensive, not only on breaking changes")
	cmd.Flags().StringVar(&report, "report", "", "write a report in this format (\"github\" annotations); to --report-file if set, otherwise printed to the terminal")
	cmd.Flags().StringVar(&reportFile, "report-file", "", "path to write the --report file to (requires --report)")
	_ = cmd.RegisterFlagCompletionFunc("report", cobra.FixedCompletions([]string{"github"}, cobra.ShellCompDirectiveNoFileComp))
	cmdmeta.SetEnum(cmd, "report", "github")
	return cmd
}

// writeDiffAnnotations emits an error annotation per breaking change and a
// warning per cost increase. They point at the new side's file when it is a
// file, else the old side's, else carry no file.
func writeDiffAnnotations(w io.Writer, oldSide, newSide diffSide, r modeldiff.Result) error {
	file := ""
	switch {
	case newSide.File != "":
		file = modeltest.RepoPath(newSide.File)
	case oldSide.File != "":
		file = modeltest.RepoPath(oldSide.File)
	}
	for _, e := range r.Breaking {
		if err := modeltest.WriteGitHubAnnotation(w, "error", file, "model diff: "+annotationSubject(e.Type, e.Relation, e.Condition), e.Detail); err != nil {
			return err
		}
	}
	for _, c := range r.Cost {
		if c.Direction != modeldiff.CostIncreased {
			continue
		}
		subject := annotationSubject(c.Type, c.Relation, "")
		msg := "cost increased for " + subject + ": " + formatCostChange(c)
		if err := modeltest.WriteGitHubAnnotation(w, "warning", file, "model diff: "+subject, msg); err != nil {
			return err
		}
	}
	return nil
}

func annotationSubject(typ, relation, condition string) string {
	switch {
	case condition != "":
		return "condition " + condition
	case typ != "" && relation != "":
		return typ + "#" + relation
	case typ != "":
		return typ
	}
	return "schema"
}
