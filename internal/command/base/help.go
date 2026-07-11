package base

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/style"
)

// helpFunc renders styled --help output in the active theme: chip section
// headers, indented content, dimmed example comments, and two-column command
// and flag lists. It is set on the root command, so cobra applies it across the
// whole command tree.
func (c *Command) helpFunc(cmd *cobra.Command, _ []string) {
	var b strings.Builder

	// Intro — the banner for the root, the path + short line for a subcommand.
	if cmd.HasParent() {
		b.WriteString("\n  " + style.Title.Render(cmd.CommandPath()))
		if cmd.Short != "" {
			b.WriteString("  " + style.Subtitle.Render("— "+cmd.Short))
		}
		b.WriteByte('\n')
		if long := strings.TrimSpace(cmd.Long); long != "" && long != cmd.Short {
			b.WriteString("\n" + indentText(long, 2) + "\n")
		}
	} else {
		b.WriteString("\n" + cmd.Long + "\n")
	}

	b.WriteString(helpSection("Usage"))
	b.WriteString(indentText(cmd.UseLine(), 4) + "\n")
	if cmd.HasAvailableSubCommands() {
		b.WriteString(indentText(cmd.CommandPath()+" [command]", 4) + "\n")
	}

	if ex := strings.TrimRight(cmd.Example, "\n"); ex != "" {
		b.WriteString(helpSection("Examples"))
		b.WriteString(styleExamples(ex) + "\n")
	}

	if cmd.HasAvailableSubCommands() {
		b.WriteString(helpSection("Commands"))
		b.WriteString(commandList(cmd))
	}

	if f := cmd.LocalFlags(); f.HasAvailableFlags() {
		b.WriteString(helpSection("Flags"))
		b.WriteString(flagList(f))
	}
	if cmd.HasParent() {
		if inh := cmd.InheritedFlags(); inh.HasAvailableFlags() {
			b.WriteString(helpSection("Global Flags"))
			b.WriteString(flagList(inh))
		}
	}

	if cmd.HasAvailableSubCommands() {
		hint := fmt.Sprintf("Run \"%s [command] --help\" for details on a command.", cmd.CommandPath())
		b.WriteString("\n" + indentText(style.Faint.Render(hint), 2) + "\n")
	}

	fmt.Fprint(cmd.OutOrStdout(), b.String())
}

// helpSection renders a chip section header, e.g. " USAGE ", with a blank line
// on either side.
func helpSection(title string) string {
	return "\n  " + style.Chip(strings.ToUpper(title), style.Primary, style.BgHighlight) + "\n\n"
}

// indentText left-pads every non-blank line by n spaces.
func indentText(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// styleExamples indents example lines and dims their `#` comments.
func styleExamples(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch t := strings.TrimSpace(l); {
		case t == "":
			lines[i] = ""
		case strings.HasPrefix(t, "#"):
			lines[i] = "    " + style.Faint.Render(t)
		default:
			lines[i] = "    " + style.Value.Render(t)
		}
	}
	return strings.Join(lines, "\n")
}

// commandList renders the available sub-commands as an aligned name/description
// two-column list.
func commandList(cmd *cobra.Command) string {
	var subs []*cobra.Command
	width := 0
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		subs = append(subs, sub)
		if len(sub.Name()) > width {
			width = len(sub.Name())
		}
	}
	var b strings.Builder
	for _, sub := range subs {
		gap := strings.Repeat(" ", width-len(sub.Name())+3)
		b.WriteString("    " + style.Key.Render(sub.Name()) + gap + style.Subtitle.Render(sub.Short) + "\n")
	}
	return b.String()
}

// flagList renders a flag set as an aligned "-x --long type / usage" two-column
// list.
func flagList(fs *pflag.FlagSet) string {
	type row struct{ left, help string }
	var rows []row
	width := 0
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		sh := "  " // no shorthand: pad where "-x" would sit
		if f.Shorthand != "" {
			sh = "-" + f.Shorthand
		}
		left := sh + " --" + f.Name
		if t := f.Value.Type(); t != "bool" {
			left += " " + t
		}
		if len(left) > width {
			width = len(left)
		}
		rows = append(rows, row{left, f.Usage})
	})
	var b strings.Builder
	for _, r := range rows {
		gap := strings.Repeat(" ", width-len(r.left)+3)
		b.WriteString("    " + style.Key.Render(r.left) + gap + style.Subtitle.Render(r.help) + "\n")
	}
	return b.String()
}
