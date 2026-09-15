package mapping

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
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
		rescue(cmd, result.data)
		return err
	}

	out := cmd.ErrOrStderr()
	output.Successf(out, "wrote %s (%s)", path, result.summary())
	nextSteps(out, path, result.tests)
	return nil
}

// nextSteps names what can be done with the file that this command cannot do
// itself. `ofga mapping` stops at init; checking a mapping and running its
// tests live in openfga's own CLI, both offline and needing neither a store nor
// a network — and nothing on the wizard's last screen says either exists.
//
// Guarded the way the status printers guard themselves. Hintf has no guard of
// its own because its usual job is remediation after an error, which --quiet
// deliberately keeps; a pointer after a success is the other case, and --quiet
// and --plain asked for the result rather than the tour.
func nextSteps(w io.Writer, path string, tests int) {
	if output.Quiet || output.Plain {
		return
	}
	output.Infof(w, "check it offline — add --model-file to check it against your model:")
	output.Hintf(w, "fga mapping validate %s", path)

	// Only worth saying when there is something to run. The wizard writes a test
	// per sample, so a user who worked through several events leaves with a suite
	// they have no particular reason to know is executable.
	if tests > 0 {
		output.Infof(w, "and run the tests the wizard embedded:")
		output.Hintf(w, "fga mapping test %s", path)
	}

	output.Infof(w, "spec: %s", languageSpecURL)
}

// rescue prints a mapping the wizard produced but could not save. By the time
// the write is attempted the TUI has been torn down and result.data is the only
// copy of it that exists: returning the error alone throws away however long the
// user spent authoring it, for a failure — a read-only directory, a full disk,
// a path they cannot write — that they could recover from in seconds if they
// still had the YAML. It goes to stdout so `> mapping.yaml` catches it, with the
// explanation on stderr beside the error cobra is about to print.
func rescue(cmd *cobra.Command, data []byte) {
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	output.Infof(cmd.ErrOrStderr(), "the mapping could not be written, so it was printed above; save it by hand to keep it")
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

// saveMapping writes data to path atomically. atomicfile deliberately leaves
// creating the destination directory to its caller; for a wizard that has just
// spent the user's time authoring a mapping, failing the save on a missing
// parent is worse than creating one, so we create it.
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

// runWizard launches the interactive editor, returning nil when the user
// cancels.
func (c *Command) runWizard(cmd *cobra.Command, path string) (*wizardResult, error) {
	profile := ""
	if r, err := c.cli.Resolve(); err == nil {
		profile = r.Profile
	}
	m := newWizard(cmd.Context(), path, profile, c.loadModel)
	final, err := tea.NewProgram(m, tea.WithContext(cmd.Context())).Run()
	if err != nil {
		return nil, err
	}
	fm, ok := final.(*wizardModel)
	if !ok || fm.cancelled || !fm.done {
		return nil, nil
	}
	return fm.result, nil
}

// loadModel reads the store's latest authorization model. The API returns
// models newest-first, so the first one is the latest.
func (c *Command) loadModel(ctx context.Context) (*openfga.AuthorizationModel, error) {
	cl, r, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, err
	}
	for mod, err := range cl.AuthorizationModels.All(ctx, nil, openfga.WithStore(r.StoreID)) {
		if err != nil {
			return nil, err
		}
		return &mod, nil
	}
	return nil, errors.New("the store has no authorization model")
}
