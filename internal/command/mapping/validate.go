package mapping

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/output"
)

type validateResult struct {
	File     string            `json:"file"`
	Valid    bool              `json:"valid"`
	Rules    int               `json:"rules"`
	Tests    int               `json:"tests"`
	Model    validateModel     `json:"model"`
	Problems []mapping.Finding `json:"problems"`
}

type validateModel struct {
	Checked bool   `json:"checked"`
	Source  string `json:"source,omitempty"`
	Types   int    `json:"types,omitempty"`
}

func (c *Command) newValidateCmd() *cobra.Command {
	var mf modelFlags
	cmd := &cobra.Command{
		Use:   "validate [file]",
		Short: "Check a mapping file, and optionally that a model has every type and relation it names",
		Long: "Compile a mapping file with openfga/mapper and report every problem with its line and column. With no file argument it uses ./mapping.yaml.\n\n" +
			"Pass --model-file (offline) or --model-from-store to also check that the authorization model " +
			"has every type, relation and user type the mapping's tuples and tuple filters name. Without either, the model is not checked.\n\n" +
			"Exit codes: 0 valid · 1 invalid or error · 2 usage · 4 network.",
		Example: `# Check ./mapping.yaml
ofga mappings validate

# Check against a model file, in CI
ofga mappings validate sync/auth0.yaml --model-file model.fga`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runValidate(cmd, targetPath(args), mf)
		},
	}
	mf.register(cmd)
	return cmd
}

func (c *Command) runValidate(cmd *cobra.Command, path string, mf modelFlags) error {
	ix, source, err := c.loadModelIndex(cmd.Context(), mf)
	if err != nil {
		return err
	}
	res := validateResult{File: path, Model: validateModel{Checked: ix != nil, Source: source}, Problems: []mapping.Finding{}}
	if ix != nil {
		res.Model.Types = len(ix.TypeNames())
	}
	f, problems, err := loadMappingFile(path)
	if err != nil {
		return err
	}
	if f == nil {
		res.Problems = problems
	} else {
		res.Rules, res.Tests = len(f.Doc.Rules), len(f.Doc.Tests)
		if found := mapping.Check(f, ix); found != nil {
			res.Problems = found
		}
	}
	res.Valid = len(res.Problems) == 0
	if err := c.printValidate(cmd, res); err != nil {
		return err
	}
	if !res.Valid {
		return clierr.Silent(clierr.CodeError)
	}
	return nil
}

func (c *Command) printValidate(cmd *cobra.Command, res validateResult) error {
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if c.cli.JSON || c.cli.YAML {
		return output.Emit(out, c.cli.YAML, res)
	}
	if err := printProblems(out, res.File, res.Problems); err != nil {
		return err
	}
	if output.Plain {
		return output.KeyValues(out, [][2]string{
			{"file", res.File},
			{"valid", strconv.FormatBool(res.Valid)},
			{"rules", strconv.Itoa(res.Rules)},
			{"tests", strconv.Itoa(res.Tests)},
			{"model_checked", strconv.FormatBool(res.Model.Checked)},
			{"problems", strconv.Itoa(len(res.Problems))},
		})
	}
	if !res.Valid {
		output.Errorf(errw, "%s: %s", res.File, mapping.Plural(len(res.Problems), "problem"))
		return nil
	}
	note := "model not checked (pass --model-file or --model-from-store)"
	if res.Model.Checked {
		note = fmt.Sprintf("checked against %s (%s)", res.Model.Source, mapping.Plural(res.Model.Types, "type"))
	}
	output.Successf(errw, "%s: %s, %s; %s", res.File, mapping.Plural(res.Rules, "rule"), mapping.Plural(res.Tests, "test"), note)
	return nil
}

// printProblems writes one path:line:col: field: message line per problem.
func printProblems(w io.Writer, path string, ps []mapping.Finding) error {
	for _, p := range ps {
		pos, field := "", ""
		if p.Line > 0 {
			pos = fmt.Sprintf(":%d:%d", p.Line, p.Column)
		}
		if p.Field != "" {
			field = output.SanitizeField(p.Field) + ": "
		}
		if _, err := fmt.Fprintf(w, "%s%s: %s%s\n", output.SanitizeField(path), pos, field, output.SanitizeField(p.Message)); err != nil {
			return err
		}
	}
	return nil
}
