// Package agent implements `ofga agent`: material for coding agents that
// drive ofga.
package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/agentskill"
	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

// Command is the `agent` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the agent command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:   "agent",
		RunE:  cli.GroupRunE,
		Short: "Material for coding agents that drive ofga",
		Long:  "Material for coding agents that drive ofga, such as the installable agent skill.",
	}
	skill := &cobra.Command{
		Use:   "skill",
		RunE:  cli.GroupRunE,
		Short: "The ofga agent skill",
		Long:  "Print or install the SKILL.md that teaches a coding agent to drive ofga.",
	}
	skill.AddCommand(
		cmdmeta.Set(c.showCmd(), cmdmeta.Meta{}),
		cmdmeta.Set(c.installCmd(), cmdmeta.Meta{Mutates: true}),
	)
	c.cmd.AddCommand(skill)
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

func (c *Command) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show",
		Short:   "Print the ofga agent skill",
		Long:    "Print the SKILL.md that ships inside this ofga binary.",
		Example: "  ofga agent skill show",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, ShowOutput{Content: string(agentskill.Content())})
			}
			_, err := cmd.OutOrStdout().Write(agentskill.Content())
			return err
		},
	}
}

func (c *Command) installCmd() *cobra.Command {
	var (
		project bool
		dir     string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the ofga agent skill",
		Long: `Install the SKILL.md that ships inside this ofga binary.

By default it goes to ~/.claude/skills/ofga/SKILL.md (Claude Code). --project
installs to ./.claude/skills/ofga/SKILL.md instead, and --dir <path> to
<path>/ofga/SKILL.md for any other agent. A file with identical content is left
alone; a different one is replaced only after confirmation or with --force.`,
		Example: `  ofga agent skill install
  ofga agent skill install --project
  ofga agent skill install --dir ~/.config/other-agent/skills --force`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirSet := cmd.Flags().Changed("dir")
			if dirSet && strings.TrimSpace(dir) == "" {
				return clierr.WithCode(clierr.CodeUsage, errors.New("--dir must not be empty"))
			}
			if project && dirSet {
				return clierr.WithCode(clierr.CodeUsage, errors.New("--project and --dir are mutually exclusive"))
			}
			path, err := skillPath(project, dir)
			if err != nil {
				return err
			}
			want := agentskill.Content()
			changed := true
			switch have, err := os.ReadFile(path); {
			case err == nil && bytes.Equal(have, want):
				changed = false
			case err == nil:
				if err := prompt.Confirm(cmd, fmt.Sprintf("replace %s?", path), force); err != nil {
					return err
				}
			case !errors.Is(err, os.ErrNotExist):
				return err
			}
			if changed {
				//nolint:gosec // a shared skills dir that other tools must read
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				// Staged, so a failed write leaves an installed skill intact.
				f, err := atomicfile.Create(path, 0o644)
				if err != nil {
					return err
				}
				defer f.Abort()
				if _, err := f.Write(want); err != nil {
					return err
				}
				if err := f.Commit(); err != nil {
					return err
				}
			}
			switch {
			case c.cli.JSON || c.cli.YAML:
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, InstallOutput{Path: path, Changed: changed})
			default:
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), path); err != nil {
					return err
				}
				if changed {
					output.Successf(cmd.ErrOrStderr(), "installed the ofga skill to %s", path)
				} else {
					output.Infof(cmd.ErrOrStderr(), "the ofga skill at %s is already up to date", path)
				}
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&project, "project", false, "install to ./.claude/skills/ofga/SKILL.md")
	cmd.Flags().StringVar(&dir, "dir", "", "install to <dir>/ofga/SKILL.md")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace a different existing file without asking")
	return cmd
}

// skillPath is the absolute SKILL.md destination.
func skillPath(project bool, dir string) (string, error) {
	var base string
	switch {
	case dir != "":
		base = dir
	case project:
		base = filepath.Join(".claude", "skills")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".claude", "skills")
	}
	return filepath.Abs(filepath.Join(base, "ofga", "SKILL.md"))
}
