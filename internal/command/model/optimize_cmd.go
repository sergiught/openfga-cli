package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/readlimit"
)

// optimizeOutput is what `model optimize` prints under --json/--yaml.
// A finding weight of -1 means an unbounded (infinite) weight.
type optimizeOutput struct {
	SchemaVersion int                `json:"schemaVersion"`
	Model         optimizeModelInfo  `json:"model"`
	Findings      []optimize.Finding `json:"findings"`
	Rewrite       *rewriteOutput     `json:"rewrite"`
	Bench         *bench.Result      `json:"bench"`
}

// optimizeModelInfo identifies the analysed input.
type optimizeModelInfo struct {
	File   string `json:"file"`
	Format string `json:"format"`
}

// rewriteOutput describes the safe rewrite: the findings applied, the
// rewritten DSL and its unified diff against the normalized input.
type rewriteOutput struct {
	Applied []string `json:"applied"`
	DSL     string   `json:"dsl"`
	Diff    string   `json:"diff"`
}

func usageHint(hint string, err error) error {
	return clierr.WithCode(clierr.CodeUsage, clierr.WithHint(hint, err))
}

func (c *Command) optimizeCmd() *cobra.Command {
	var (
		write   bool
		outPath string
		tiers   []string
	)
	cmd := &cobra.Command{
		Use:   "optimize <model-file|->",
		Short: "Find equivalence-preserving rewrites and other optimizations for a model",
		Example: `  ofga model optimize model.fga
  ofga model optimize model.fga --write
  ofga model optimize model.json --output-file optimized.fga
  ofga model optimize model.fga --tier safe,caller
  cat model.fga | ofga model optimize - --json`,
		Long: "Analyze an authorization model and report optimization findings in three tiers: safe (rewrites that never change any Check answer), caller (changes in how callers use the model) and tuple (changes in stored tuples). The safe rewrite is shown as a unified diff.\n\n" +
			"Input: a DSL or JSON model file, or '-' for stdin. The format is auto-detected.\n\n" +
			"Writing: --write overwrites the model file with the rewritten DSL (DSL files only; it reformats the file and drops comments). --output-file writes the rewritten DSL to another path and also accepts JSON or stdin input. --tier filters only what is reported, never what is written.\n\n" +
			"Weights: in --json/--yaml output a finding weight of -1 means unbounded (infinite); human output renders it as inf.\n\n" +
			"Exit codes: 0 success · 1 error · 2 usage (including an invalid model).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			stdin := path == "-"
			want, err := parseTiers(tiers)
			if err != nil {
				return err
			}
			if write && outPath != "" {
				return usageHint("use either --write (in place) or --output-file <path>", errors.New("--write and --output-file are mutually exclusive"))
			}
			if write && stdin {
				return usageHint("use --output-file <path> to write the rewritten model", errors.New("--write cannot rewrite stdin"))
			}

			var data []byte
			if stdin {
				data, err = readlimit.All(cmd.InOrStdin(), readlimit.Document, "model from stdin")
			} else {
				data, err = readlimit.File(path, readlimit.Document, "model file")
			}
			if err != nil {
				return err
			}
			format := "dsl"
			if isJSONModel(data) {
				format = "json"
			}
			if write && format == "json" {
				return usageHint("use --output-file <path> to write the rewritten DSL",
					errors.New("--write cannot rewrite a JSON model in place (the rewrite is emitted as DSL)"))
			}

			lm, err := modeltest.LoadModelBytes(data)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("load model: %w", err))
			}
			report, err := optimize.Analyze(lm.Proto)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}

			findings := filterTiers(report.Findings, want)
			out := optimizeOutput{
				SchemaVersion: 1,
				Model:         optimizeModelInfo{File: path, Format: format},
				Findings:      findings,
			}
			if rw := report.Rewrite; rw != nil {
				before, err := rw.Before.DSL()
				if err != nil {
					return fmt.Errorf("render original model: %w", err)
				}
				after, err := rw.After.DSL()
				if err != nil {
					return fmt.Errorf("render rewritten model: %w", err)
				}
				ids := make([]string, 0, len(rw.Applied))
				for _, f := range rw.Applied {
					ids = append(ids, f.ID)
				}
				out.Rewrite = &rewriteOutput{Applied: ids, DSL: after, Diff: unifiedDiff(path, before, after)}
			}

			if c.cli.JSON || c.cli.YAML {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, out); err != nil {
					return err
				}
			} else if err := renderOptimize(cmd.OutOrStdout(), findings, out.Rewrite); err != nil {
				return err
			}

			if out.Rewrite == nil {
				output.Infof(cmd.ErrOrStderr(), "no safe rewrites; %d recommendations", len(report.Findings))
				return nil
			}
			dest := outPath
			if write {
				dest, err = filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
			}
			if dest == "" {
				return nil
			}
			if err := writeFileAtomic(dest, out.Rewrite.DSL); err != nil {
				return err
			}
			if write {
				output.Warnf(cmd.ErrOrStderr(), "--write reformats the file and drops comments")
			}
			output.Successf(cmd.ErrOrStderr(), "wrote rewritten model to %s", dest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "overwrite the model file with the rewritten DSL (reformats and drops comments; DSL files only)")
	cmd.Flags().StringVar(&outPath, "output-file", "", "write the rewritten DSL to this path (nothing is written when no safe rewrite applies)")
	cmd.Flags().StringSliceVar(&tiers, "tier", nil, "tiers to report: safe, caller, tuple (default all; never changes what is written)")
	_ = cmd.RegisterFlagCompletionFunc("tier", cobra.FixedCompletions([]string{"safe", "caller", "tuple"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagFilename("output-file")
	return cmd
}

// parseTiers validates --tier; an empty list selects every tier (nil).
func parseTiers(in []string) (map[optimize.Tier]bool, error) {
	if len(in) == 0 {
		return nil, nil
	}
	want := map[optimize.Tier]bool{}
	for _, s := range in {
		switch t := optimize.Tier(strings.TrimSpace(s)); t {
		case optimize.TierSafe, optimize.TierCaller, optimize.TierTuple:
			want[t] = true
		default:
			return nil, usageHint("valid tiers: safe, caller, tuple", fmt.Errorf("unknown --tier %q", s))
		}
	}
	return want, nil
}

// filterTiers keeps report order; a nil want keeps everything. The result is
// never nil so JSON prints [] rather than null.
func filterTiers(in []optimize.Finding, want map[optimize.Tier]bool) []optimize.Finding {
	out := make([]optimize.Finding, 0, len(in))
	for _, f := range in {
		if want == nil || want[f.Tier] {
			out = append(out, f)
		}
	}
	return out
}

// writeFileAtomic replaces dest, preserving its permissions when it exists.
func writeFileAtomic(dest, content string) error {
	perm := os.FileMode(0o644)
	if st, err := os.Stat(dest); err == nil {
		perm = st.Mode().Perm()
	}
	f, err := atomicfile.Create(dest, perm)
	if err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	defer f.Abort()
	if _, err := f.Write([]byte(content)); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return f.Commit()
}
