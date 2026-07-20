package model

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/style"
)

// scaffoldFiles is the workspace `model test init` writes: a manifest, a small
// model, a fixture, and a test that passes against them, so a new user can run
// `ofga model test` immediately and see a green run to learn the format from.
var scaffoldFiles = []struct {
	path    string
	content string
}{
	{"ofga.yaml", `# ofga model-test workspace. Run: ofga model test
#
# Editor completion/validation: generate the schema once with
#   ofga model test schema > workspace.schema.json
# and the modeline below binds this manifest to it.
# yaml-language-server: $schema=./workspace.schema.json#manifest
version: 1
model: ./model.fga
fixtures:
  - fixtures/*.yaml
tests:
  - tests/**/*.test.yaml
`},
	{"model.fga", `model
  schema 1.1

type user

type document
  relations
    define owner: [user]
    define viewer: [user] or owner
`},
	{filepath.Join("fixtures", "example.yaml"), `# Tuples seeded before the tests run.
- user: user:anne
  relation: owner
  object: document:readme
- user: user:bob
  relation: viewer
  object: document:readme
`},
	{filepath.Join("tests", "example.test.yaml"), `# yaml-language-server: $schema=../workspace.schema.json#testFile
#
# Use the tuples registered by fixtures/example.yaml (referenced by file stem).
# See the full format any time with: ofga model test schema
fixtures: [example]
tests:
  - name: owner-can-view
    description: an owner is implicitly a viewer
    check:
      - user: user:anne
        object: document:readme
        assertions:
          owner: true
          viewer: true
  - name: viewer-is-not-owner
    check:
      - user: user:bob
        object: document:readme
        assertions:
          viewer: true
          owner: false
  # list_objects asserts the exact set of objects a user has a relation to.
  # Uncomment to try it (anne owns document:readme, so she can view it):
  # - name: anne-can-view-documents
  #   list_objects:
  #     - user: user:anne
  #       type: document
  #       assertions:
  #         viewer: [document:readme]
`},
}

// testInitCmd scaffolds a runnable model-test workspace.
func (c *Command) testInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a runnable model-test workspace (ofga.yaml + a sample model, fixture, and test)",
		Long: "Write a minimal ofga.yaml workspace — a model, a fixture, and a test that passes against them — into dir (default: current directory). " +
			"Run `ofga model test` in that directory to see a green run and learn the format. Existing files are left untouched unless --force is given.",
		Example: `  ofga model test init
  ofga model test init ./ws
  ofga model test init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return scaffoldWorkspace(cmd, dir, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return cmd
}

// scaffoldWorkspace writes the scaffold files under dir, refusing to clobber an
// existing file unless force is set.
func scaffoldWorkspace(cmd *cobra.Command, dir string, force bool) error {
	out := cmd.OutOrStdout()
	for _, f := range scaffoldFiles {
		dest := filepath.Join(dir, f.path)
		if !force {
			if _, err := os.Stat(dest); err == nil {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("%s already exists; use --force to overwrite", dest))
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		fmt.Fprintf(out, "  %s %s\n", style.Success.Render("created"), dest)
	}
	fmt.Fprintf(out, "\n%s\n", style.Faint.Render("Scaffolded a model-test workspace. Run `ofga model test` here to try it."))
	return nil
}
