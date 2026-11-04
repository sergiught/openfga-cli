package model

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/optimize/benchfmt"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/readlimit"
)

// optimizeOutput is what `model optimize` prints under --json/--yaml.
// A finding weight of -1 means an unbounded (infinite) weight.
type optimizeOutput struct {
	SchemaVersion       int                     `json:"schemaVersion"`
	Model               optimizeModelInfo       `json:"model"`
	Findings            []optimize.Finding      `json:"findings"`
	Rewrite             *rewriteOutput          `json:"rewrite"`
	Gated               []optimize.GatedFinding `json:"gated,omitempty" jsonschema_description:"Safe rewrites that were derived but not applied, with the gate reason."`
	SafeRewritesBlocked string                  `json:"safeRewritesBlocked,omitempty" jsonschema_description:"Why no safe rewrite was applied at all; set only when an exclusion subtracts a recursive relation."`
	Bench               *bench.Result           `json:"bench"`
}

// optimizeModelInfo identifies the analysed input.
type optimizeModelInfo struct {
	File   string `json:"file"`
	Format string `json:"format"`
}

// rewriteOutput describes the safe rewrite: the findings applied, the
// rewritten DSL and its unified diff against the normalized input.
type rewriteOutput struct {
	Applied []string `json:"applied" jsonschema_description:"Keys of the applied safe findings, in order; equals findings[].key."`
	DSL     string   `json:"dsl"`
	Diff    string   `json:"diff"`
}

func usageHint(hint string, err error) error {
	return clierr.WithCode(clierr.CodeUsage, clierr.WithHint(hint, err))
}

func (c *Command) optimizeCmd() *cobra.Command {
	var (
		write    bool
		outPath  string
		runBench bool
		wsPath   string
		synth    bool
		seed     uint64
		depth    int
		iters    int
		tiers    []string
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
			"Exit codes: 0 success · 1 error · 2 usage (including an invalid model) · 3 benchmark found a check whose answer changed. A changed answer is reported as unstable (engine nondeterminism) and does not set exit code 3 only when the original model's answer varies when re-run, or the check's relation cannot reach any relation the rewrite changed.",
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
			toStdout := outPath == "-"
			if toStdout && (c.cli.JSON || c.cli.YAML) {
				flagName := "--json"
				if !c.cli.JSON {
					flagName = "--yaml"
				}
				return usageHint("drop --json/--yaml, or write to a file with --output-file <path>", errors.New("--output-file - and "+flagName+" both write to stdout; pick one"))
			}
			if write && stdin {
				return usageHint("use --output-file <path> to write the rewritten model", errors.New("--write cannot rewrite stdin"))
			}

			if err := validateBenchFlags(cmd, runBench, wsPath, synth, depth, iters); err != nil {
				return err
			}

			var data []byte
			if stdin {
				stdinHint(cmd.ErrOrStderr(), term.IsTerminal(os.Stdin.Fd()))
				data, err = readlimit.All(cmd.InOrStdin(), readlimit.Document, "model from stdin")
			} else {
				data, err = readlimit.File(path, readlimit.Document, "model file")
			}
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(data)) == 0 {
				if stdin {
					return usageHint("pipe a model in, or pass a model file", errors.New("model from stdin is empty"))
				}
				return usageHint("pass a non-empty .fga or .json model file", fmt.Errorf("model file is empty: %s", path))
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
				if errors.Is(err, optimize.ErrInvalidModel) {
					return clierr.WithCode(clierr.CodeUsage, err)
				}
				return err
			}

			findings := filterTiers(report.Findings, want)
			out := optimizeOutput{
				SchemaVersion:       1,
				Model:               optimizeModelInfo{File: path, Format: format},
				Findings:            findings,
				SafeRewritesBlocked: report.Blocked,
				Gated:               report.Gated,
			}
			if report.Blocked != "" {
				output.Warnf(cmd.ErrOrStderr(), "safe rewrites disabled: %s", report.Blocked)
			}
			if rw := report.Rewrite; rw != nil {
				normalizedBefore, err := rw.Before.DSL()
				if err != nil {
					return fmt.Errorf("render original model: %w", err)
				}
				after, err := rw.After.DSL()
				if err != nil {
					return fmt.Errorf("render rewritten model: %w", err)
				}
				before, label := normalizedBefore, path
				switch {
				case stdin:
					label = "model"
				case format == "json":
					label = path + " (normalized)"
				default:
					// Diff the bytes the user has, so the preview applies to
					// their file (comments, relation order and all).
					before = string(data)
				}
				reformats := (format == "dsl" && !stdin && before != normalizedBefore) || sameFile(outPath, path)
				if reformats {
					output.Warnf(cmd.ErrOrStderr(), "%s", reformatWarning)
				}
				ids := make([]string, 0, len(rw.Applied))
				for _, f := range rw.Applied {
					ids = append(ids, f.Key)
				}
				out.Rewrite = &rewriteOutput{Applied: ids, DSL: after, Diff: optimize.UnifiedDiff(label, before, after)}
			}

			structured := c.cli.JSON || c.cli.YAML
			// Human mode prints findings and the diff before the (slow)
			// benchmark so the user has something to read while it runs.
			if !structured && !toStdout {
				gated := out.Gated
				if want != nil && !want[optimize.TierSafe] {
					gated = nil // the safe tier is not requested
				}
				if err := renderOptimize(cmd.OutOrStdout(), findings, gated, out.Rewrite); err != nil {
					return err
				}
			}

			if runBench {
				var afterModel *openfgav1.AuthorizationModel
				if report.Rewrite != nil {
					afterModel = report.Rewrite.After.Proto
				}
				src, serverOpts, err := benchSource(cmd, lm.Proto, path, stdin, wsPath, synth, seed, depth)
				if err != nil {
					return err
				}
				if !structured && !toStdout && !output.Quiet && !output.Terse {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "benchmarking %d checks × %d runs…\n", len(src.Checks), iters)
				}
				res, err := benchRunFn(cmd.Context(), lm.Proto, afterModel, src, bench.Options{Iterations: iters, ServerOpts: serverOpts})
				if err != nil {
					if src.Kind == "workspace" && errors.Is(err, bench.ErrSetup) {
						return usageHint("pass --synth to benchmark with synthesized tuples", fmt.Errorf("workspace tuples do not fit this model: %w", err))
					}
					return fmt.Errorf("benchmark: %w", err)
				}
				if report.Rewrite != nil {
					switch src.Kind {
					case "synthesis":
						res.Unexercised = optimize.Unexercised(report.Rewrite.After, report.Rewrite.Applied, res.Skipped)
					case "workspace":
						checked := make([]string, 0, len(src.Checks))
						for _, c := range src.Checks {
							objType, _, _ := strings.Cut(c.Object, ":")
							checked = append(checked, objType+"#"+c.Relation)
						}
						res.Unexercised = optimize.UnexercisedByChecks(report.Rewrite.After, report.Rewrite.Applied, checked)
					}
				}
				out.Bench = res
			}

			if structured {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, out); err != nil {
					return err
				}
			} else if !toStdout {
				if err := benchfmt.Render(cmd.OutOrStdout(), out.Bench); err != nil {
					return err
				}
			}

			if out.Bench != nil && len(out.Bench.Mismatches) > 0 {
				return clierr.WithCode(clierr.CodeTestFailed, fmt.Errorf(
					"benchmark found %s whose answer changed; rewrite was NOT written — please report this model bug",
					benchfmt.Plural(len(out.Bench.Mismatches), "check")))
			}

			dest := outPath
			if write {
				dest, err = filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
			}
			if out.Rewrite == nil {
				if write {
					output.Infof(cmd.ErrOrStderr(), "no safe rewrites; nothing written to %s", dest)
					return nil
				}
				if dest != "" {
					// --output-file always yields the destination: the input
					// bytes for DSL, otherwise its DSL rendering.
					unchanged := string(data)
					if format == "json" {
						m, err := optimize.NewModel(lm.Proto)
						if err != nil {
							return fmt.Errorf("render original model: %w", err)
						}
						if unchanged, err = m.DSL(); err != nil {
							return fmt.Errorf("render original model: %w", err)
						}
					}
					shown := dest
					if toStdout {
						shown = "stdout"
						if _, err := io.WriteString(cmd.OutOrStdout(), unchanged); err != nil {
							return err
						}
					} else if err := writeFileAtomic(dest, unchanged); err != nil {
						return err
					}
					output.Infof(cmd.ErrOrStderr(), "no safe rewrites; wrote the unchanged model to %s", shown)
					return nil
				}
				if label := tierLabel(tiers); label != "" && len(findings) == 0 {
					output.Infof(cmd.ErrOrStderr(), "no safe rewrites; 0 %s recommendations", label)
					return nil
				}
				output.Infof(cmd.ErrOrStderr(), "no safe rewrites; %s", benchfmt.Plural(len(findings), "recommendation"))
				return nil
			}
			if toStdout {
				if _, err := io.WriteString(cmd.OutOrStdout(), out.Rewrite.DSL); err != nil {
					return err
				}
				output.Successf(cmd.ErrOrStderr(), "wrote rewritten model to stdout")
				return nil
			}
			if dest == "" {
				return nil
			}
			if err := writeFileAtomic(dest, out.Rewrite.DSL); err != nil {
				return err
			}
			output.Successf(cmd.ErrOrStderr(), "wrote rewritten model to %s", dest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "overwrite the model file with the rewritten DSL (reformats and drops comments; DSL files only)")
	cmd.Flags().StringVar(&outPath, "output-file", "", "write the rewritten DSL to this path; - means stdout")
	cmd.Flags().StringSliceVar(&tiers, "tier", nil, "tiers to report: safe, caller, tuple, all (default all; never changes what is written)")
	_ = cmd.RegisterFlagCompletionFunc("tier", cobra.FixedCompletions([]string{"safe", "caller", "tuple", "all"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagFilename("output-file")
	cmd.Flags().BoolVar(&runBench, "bench", false, "benchmark the model before/after the rewrite on an embedded server (exit 3 and no write if any check result changes)")
	cmd.Flags().StringVar(&wsPath, "workspace", "", "workspace directory supplying tuples and checks for --bench (default: ofga.yaml found above the model file)")
	cmd.Flags().BoolVar(&synth, "synth", false, "with --bench, synthesize tuples even when a workspace is found")
	cmd.Flags().Uint64Var(&seed, "seed", 1, "with --bench, synthesis seed")
	cmd.Flags().IntVar(&depth, "depth", 6, "with --bench, synthesized parent-chain depth")
	cmd.Flags().IntVar(&iters, "iterations", 100, "with --bench, timed samples per check")
	_ = cmd.MarkFlagDirname("workspace")
	return cmd
}

// benchRunFn is bench.Run; tests replace it to inject results.
var benchRunFn = bench.Run

// validateBenchFlags rejects bench-only flags without --bench and bad values.
func validateBenchFlags(cmd *cobra.Command, runBench bool, wsPath string, synth bool, depth, iters int) error {
	if !runBench {
		for _, f := range []string{"workspace", "synth", "seed", "depth", "iterations"} {
			if cmd.Flags().Changed(f) {
				return usageHint("add --bench", fmt.Errorf("--%s only applies with --bench", f))
			}
		}
		return nil
	}
	if wsPath != "" && synth {
		return usageHint("use either --workspace <dir> or --synth", errors.New("--workspace and --synth are mutually exclusive"))
	}
	if depth < 1 {
		return clierr.WithCode(clierr.CodeUsage, errors.New("--depth must be at least 1"))
	}
	if iters < 1 {
		return clierr.WithCode(clierr.CodeUsage, errors.New("--iterations must be at least 1"))
	}
	return nil
}

// benchSource picks the benchmark tuples and checks: an explicit or
// auto-detected workspace, else synthesis. It also returns the workspace's
// embedded-server options.
const reformatWarning = "rewrite reformats the model: comments are dropped and relations are sorted"

// sameFile reports whether out names the same existing file as in.
func sameFile(out, in string) bool {
	if out == "" || in == "-" {
		return false
	}
	a, err := os.Stat(out)
	if err != nil {
		return false
	}
	b, err := os.Stat(in)
	return err == nil && os.SameFile(a, b)
}

func benchSource(cmd *cobra.Command, m *openfgav1.AuthorizationModel, path string, stdin bool, wsPath string, synth bool, seed uint64, depth int) (*bench.Source, map[string]any, error) {
	synthesize := func() (*bench.Source, map[string]any, error) {
		src, err := bench.Synthesize(m, bench.SynthOptions{Seed: seed, Depth: depth})
		if builderMsg, ok := optimize.NoWeightedGraphReason(err); ok {
			return nil, nil, usageHint(fmt.Sprintf("this model's weighted graph cannot be built (%s), so tuples cannot be synthesized; benchmark with --workspace instead", builderMsg), optimize.ErrNoWeightedGraph)
		}
		return src, nil, err
	}
	if synth {
		return synthesize()
	}
	dir := wsPath
	if dir == "" {
		if stdin {
			return synthesize()
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		dir = filepath.Dir(abs)
	}
	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		if wsPath == "" && errors.Is(err, modeltest.ErrWorkspaceNotFound) {
			return synthesize()
		}
		return nil, nil, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("load workspace: %w", err))
	}
	src, notices, err := bench.FromWorkspace(ws)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace benchmark source: %w", err)
	}
	for _, n := range notices {
		output.Warnf(cmd.ErrOrStderr(), "%s", n)
	}
	var serverOpts map[string]any
	if ws.Manifest != nil {
		serverOpts = ws.Manifest.Server
		if ws.Manifest.Model != "" && !stdin {
			wsModel := filepath.Clean(filepath.Join(ws.Root, ws.Manifest.Model))
			given, err := filepath.Abs(path)
			if err == nil && filepath.Clean(given) != wsModel {
				output.Warnf(cmd.ErrOrStderr(), "workspace model is %s but optimizing %s; running the workspace's tuples and checks against the given model", wsModel, given)
			}
		}
	}
	return src, serverOpts, nil
}

// parseTiers validates --tier; an empty list selects every tier (nil).
func parseTiers(in []string) (map[optimize.Tier]bool, error) {
	if len(in) == 0 {
		return nil, nil
	}
	want := map[optimize.Tier]bool{}
	for _, s := range in {
		switch t := optimize.Tier(strings.ToLower(strings.TrimSpace(s))); t {
		case "all":
			want[optimize.TierSafe], want[optimize.TierCaller], want[optimize.TierTuple] = true, true, true
		case optimize.TierSafe, optimize.TierCaller, optimize.TierTuple:
			want[t] = true
		default:
			return nil, usageHint("valid tiers: safe, caller, tuple, all", fmt.Errorf("unknown --tier %q", s))
		}
	}
	return want, nil
}

// tierLabel joins the tiers the user passed, lowercased and de-duplicated in
// the order given, with "/". It is empty when "all" is among them.
func tierLabel(in []string) string {
	var names []string
	seen := map[string]bool{}
	for _, s := range in {
		n := strings.ToLower(strings.TrimSpace(s))
		if n == "all" {
			return ""
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return strings.Join(names, "/")
}

// stdinHint tells an interactive user that the command is waiting on stdin.
func stdinHint(w io.Writer, isTTY bool) {
	if isTTY {
		output.Infof(w, "reading model from stdin (press Ctrl-D to finish)")
	}
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
