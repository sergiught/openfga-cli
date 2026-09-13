package mapping

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/output"
)

// defaultFile is the mapping file name `ofga mapping init` writes when the user
// names none.
const defaultFile = "mapping.yaml"

// languageSpecURL is printed whenever the wizard cannot run, so a user who has
// to write the file by hand knows where the grammar is documented.
const languageSpecURL = "https://github.com/openfga/mapper/blob/main/docs/language-spec.md"

func (c *Command) newInitCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init [file]",
		Short: "Create a mapping file interactively",
		Long: "Walk through creating an openfga/mapper mapping file: pick an event, describe the " +
			"tuples it should produce, and watch the YAML and its output update as you go.\n\n" +
			"The wizard needs an interactive terminal. The file defaults to " + defaultFile + ".",
		Example: `# Create ./mapping.yaml
ofga mapping init

# Create a named file, overwriting it if it exists
ofga mapping init auth0.yaml --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runInit(cmd, args, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the file if it already exists")
	return cmd
}

func (c *Command) runInit(cmd *cobra.Command, args []string, force bool) error {
	path := targetPath(args)

	// Existence is checked before interactivity: a user who typo'd over a real
	// file needs to hear about the file, not about their terminal.
	if !force {
		if _, err := os.Stat(path); err == nil {
			return clierr.WithCode(clierr.CodeUsage,
				fmt.Errorf("%s already exists; use --force to overwrite", path))
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check %s: %w", path, err)
		}
	}

	if !wizardEligible(cmd, c.cli) {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"ofga mapping init needs an interactive terminal; write the mapping by hand instead (see %s)",
			languageSpecURL))
	}

	result, err := c.runWizard(cmd, path)
	if err != nil {
		return err
	}
	if result == nil {
		output.Infof(cmd.ErrOrStderr(), "setup cancelled; no changes made")
		return nil
	}

	if err := saveMapping(path, result.data); err != nil {
		return err
	}

	out := cmd.ErrOrStderr()
	output.Successf(out, "wrote %s (%s)", path, result.summary())
	output.Infof(out, "next: edit by hand or re-run `ofga mapping init --force`")
	output.Infof(out, "spec: %s", languageSpecURL)
	return nil
}

// wizardEligible reports whether an interactive wizard can run: structured
// output and --no-input rule it out, and both ends of the pipe must be a
// terminal. Mirrors configcmd's gate so the two wizards behave alike.
func wizardEligible(cmd *cobra.Command, c *cli.CLI) bool {
	if c.NoInput || c.JSON || c.YAML {
		return false
	}
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(in.Fd()) {
		return false
	}
	return term.IsTerminal(os.Stdout.Fd())
}

// targetPath resolves the optional file argument.
func targetPath(args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	return defaultFile
}

// saveMapping writes data to path atomically, creating parent directories —
// atomicfile needs the destination directory to exist.
func saveMapping(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	f, err := atomicfile.Create(path, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Abort()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Commit(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// wizardResult is what a completed wizard hands back to the command.
type wizardResult struct {
	data   []byte
	rules  int
	tuples int
	tests  int
}

func (r *wizardResult) summary() string {
	return fmt.Sprintf("%s, %s, %s",
		plural(r.rules, "rule"), plural(r.tuples, "tuple"), plural(r.tests, "embedded test"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// runWizard launches the interactive editor. It returns nil when the user
// cancels. Replaced with the bubbletea program in the next task.
func (c *Command) runWizard(_ *cobra.Command, _ string) (*wizardResult, error) {
	return nil, nil
}
