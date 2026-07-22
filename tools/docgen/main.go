// Command docgen generates the per-command MDX reference pages for the docs
// site (docs/site/src/content/docs/reference) from the live cobra tree, so the
// published reference can never drift from the binary. Pages are wiped and
// fully regenerated on every run; never hand-edit the output.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/config"
)

func main() {
	out := flag.String("out", "docs/site/src/content/docs/reference", "output directory for generated MDX pages")
	rec := flag.String("recordings", "docs/site/public/recordings", "directory holding <slug>.webm demo recordings")
	flag.Parse()
	if err := run(*out, *rec); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

func run(out, rec string) error {
	root := buildTree()
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "index.mdx"), renderIndex(root), 0o644); err != nil {
		return err
	}
	for i, cmd := range visibleCommands(root) {
		if err := emit(cmd, out, rec, i+1); err != nil {
			return err
		}
	}
	return nil
}

// buildTree constructs the full ofga command tree without loading any user
// config. InitDefaultCompletionCmd is normally called during Execute, so call
// it here to make the completion subcommands visible to the walk.
func buildTree() *cobra.Command {
	c := cli.New(log.New(io.Discard), config.New(), "docs")
	root := base.New(c).Command()
	root.InitDefaultCompletionCmd()
	return root
}

// emit writes the page(s) for one top-level command: either a single leaf
// page, or a group index plus one page per visible child.
func emit(cmd *cobra.Command, out, rec string, order int) error {
	children := visibleCommands(cmd)
	if len(children) == 0 {
		page := renderLeafPage(cmd, hasRecording(rec, cmd), 3, order)
		return os.WriteFile(filepath.Join(out, cmd.Name()+".mdx"), page, 0o644)
	}
	dir := filepath.Join(out, cmd.Name())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.mdx"), renderGroupIndex(cmd, order), 0o644); err != nil {
		return err
	}
	for i, child := range children {
		page := renderLeafPage(child, hasRecording(rec, child), 4, i+1)
		if err := os.WriteFile(filepath.Join(dir, child.Name()+".mdx"), page, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func hasRecording(rec string, cmd *cobra.Command) bool {
	_, err := os.Stat(filepath.Join(rec, slug(cmd)+".webm"))
	return err == nil
}

// slug turns "ofga stores create" into "stores-create".
func slug(cmd *cobra.Command) string {
	path := strings.Fields(cmd.CommandPath())
	return strings.Join(path[1:], "-")
}

func visibleCommands(cmd *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{}
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		out = append(out, c)
	}
	return out
}

// escapeMDX escapes characters MDX would treat as JSX in prose positions.
func escapeMDX(s string) string {
	s = strings.ReplaceAll(s, "{", `\{`)
	return strings.ReplaceAll(s, "<", `\<`)
}

// bareURLPattern matches bare http(s) URLs (e.g. inside a flag's default-value
// hint) so they can be wrapped as inline code instead of left as plain text,
// which Markdown would otherwise autolink.
var bareURLPattern = regexp.MustCompile(`https?://[^\s)]+`)

// escapeBareURLs wraps bare URLs in backticks so table cells render them as
// inline code rather than live links.
func escapeBareURLs(s string) string {
	return bareURLPattern.ReplaceAllString(s, "`$0`")
}

// escapeCell prepares a string for use inside a Markdown table cell: escape
// MDX-sensitive characters, escape the table's own `|` delimiter, then wrap
// any bare URL as inline code.
func escapeCell(s string) string {
	return escapeBareURLs(strings.ReplaceAll(escapeMDX(s), "|", `\|`))
}

func frontmatter(cmd *cobra.Command, order int) string {
	return fmt.Sprintf("---\ntitle: %q\ndescription: %q\nsidebar:\n  order: %d\n---\n",
		cmd.CommandPath(), cmd.Short, order)
}

func renderLeafPage(cmd *cobra.Command, recording bool, ups, order int) []byte {
	var b strings.Builder
	b.WriteString(frontmatter(cmd, order))
	b.WriteString("\n")
	if recording {
		rel := strings.Repeat("../", ups) + "components/CommandDemo.astro"
		fmt.Fprintf(&b, "import CommandDemo from '%s';\n\n", rel)
		fmt.Fprintf(&b, "<CommandDemo slug=%q />\n\n", slug(cmd))
	}
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	b.WriteString(escapeMDX(strings.TrimSpace(desc)) + "\n")

	b.WriteString("\n## Usage\n\n```sh\n" + cmd.UseLine() + "\n```\n")

	if cmd.Example != "" {
		b.WriteString("\n## Examples\n\n```sh\n" + strings.TrimRight(cmd.Example, "\n") + "\n```\n")
	}

	if rows := flagRows(cmd.NonInheritedFlags()); len(rows) > 0 {
		b.WriteString("\n## Flags\n\n| Flag | Description | Default |\n| --- | --- | --- |\n")
		for _, r := range rows {
			b.WriteString(r + "\n")
		}
	}

	if global := cmd.InheritedFlags(); global.HasAvailableFlags() {
		b.WriteString("\n<details>\n<summary>Global flags</summary>\n\n```\n")
		b.WriteString(strings.TrimRight(global.FlagUsages(), "\n"))
		b.WriteString("\n```\n\n</details>\n")
	}

	if cmd.HasParent() && cmd.Parent().HasParent() {
		siblings := []string{}
		for _, s := range visibleCommands(cmd.Parent()) {
			if s == cmd {
				continue
			}
			siblings = append(siblings, fmt.Sprintf("- [%s](../%s/)", escapeMDX(s.CommandPath()), s.Name()))
		}
		if len(siblings) > 0 {
			b.WriteString("\n## Related\n\n" + strings.Join(siblings, "\n") + "\n")
		}
	}
	return []byte(b.String())
}

func flagRows(fs *pflag.FlagSet) []string {
	rows := []string{}
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Hidden {
			return
		}
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + ", --" + f.Name + "`"
		}
		def := f.DefValue
		if def == "" {
			def = " "
		} else {
			def = "`" + def + "`"
		}
		usage := escapeCell(f.Usage)
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |", name, usage, def))
	})
	return rows
}

func renderGroupIndex(cmd *cobra.Command, order int) []byte {
	var b strings.Builder
	b.WriteString(frontmatter(cmd, order))
	b.WriteString("\n")
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	b.WriteString(escapeMDX(strings.TrimSpace(desc)) + "\n")
	b.WriteString("\n| Command | Description |\n| --- | --- |\n")
	for _, child := range visibleCommands(cmd) {
		fmt.Fprintf(&b, "| [%s](%s/) | %s |\n", child.Name(), child.Name(), escapeCell(child.Short))
	}
	return []byte(b.String())
}

func renderIndex(root *cobra.Command) []byte {
	var b strings.Builder
	b.WriteString("---\ntitle: \"Command reference\"\ndescription: \"Every ofga command, with a demo recording for each.\"\nsidebar:\n  order: 0\n  label: Overview\n---\n\n")
	b.WriteString("Generated from the CLI itself with `make docs-reference` — it cannot drift from the binary.\n")
	b.WriteString("\n| Command | Description |\n| --- | --- |\n")
	for _, cmd := range visibleCommands(root) {
		// Starlight routes both reference/api.mdx and reference/stores/index.mdx
		// to a trailing-slash URL, so leaves and groups share the same link form.
		fmt.Fprintf(&b, "| [%s](%s/) | %s |\n", cmd.Name(), cmd.Name(), escapeCell(cmd.Short))
	}
	return []byte(b.String())
}
