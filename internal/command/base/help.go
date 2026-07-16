package base

import (
	"fmt"
	"image/color"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/style"
)

// helpFunc renders styled --help output in the active theme: bold section
// headings, a raised terminal block behind the usage/examples commands, and
// two-column command and flag lists. It is set on the root command, so cobra
// applies it across the whole command tree.
func (c *Command) helpFunc(cmd *cobra.Command, _ []string) {
	// Cobra renders --help after parsing flags but before PersistentPreRunE.
	// Apply color/theme flags here too so --no-color/--theme affect help.
	c.applyEnvironment()

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
		b.WriteString("\n" + indentText(cmd.Long, 2) + "\n")
	}

	// Usage + examples render as raised shell blocks that share one width.
	usage := []blockLine{{cmd.UseLine(), style.Fg}}
	if cmd.HasAvailableSubCommands() {
		usage = append(usage, blockLine{cmd.CommandPath() + " [command]", style.Fg})
	}
	examples := exampleLines(cmd.Example)
	width := blockWidth(append(append([]blockLine{}, usage...), examples...))

	b.WriteString(sectionHead("Usage"))
	b.WriteString(shellBlock(usage, width))

	if len(examples) > 0 {
		b.WriteString(sectionHead("Examples"))
		b.WriteString(shellBlock(examples, width))
	}

	if cmd.HasAvailableSubCommands() {
		b.WriteString(sectionHead("Commands"))
		b.WriteString(commandList(cmd))
	}

	if f := cmd.LocalFlags(); f.HasAvailableFlags() {
		b.WriteString(sectionHead("Flags"))
		b.WriteString(flagList(f))
	}
	if cmd.HasParent() {
		if inh := cmd.InheritedFlags(); inh.HasAvailableFlags() {
			b.WriteString(sectionHead("Global Flags"))
			b.WriteString(flagList(inh))
		}
	}

	if !cmd.HasParent() {
		b.WriteString(sectionHead("Environment"))
		b.WriteString(envList())
	}
	b.WriteString(sectionHead("Documentation"))
	b.WriteString("    https://github.com/sergiught/openfga-cli#readme\n")
	if !cmd.HasParent() {
		b.WriteString(sectionHead("Support"))
		b.WriteString("    https://github.com/sergiught/openfga-cli/issues\n")
	}

	if cmd.HasAvailableSubCommands() {
		hint := fmt.Sprintf("Run \"%s [command] --help\" for details on a command.", cmd.CommandPath())
		b.WriteString("\n" + indentText(style.Faint.Render(hint), 2) + "\n")
	}

	fmt.Fprint(cmd.OutOrStdout(), b.String())
}

// envList renders the environment variables ofga honors, aligned like the flag
// list. FGA_* aliases are accepted for compatibility with the official CLI.
func envList() string {
	rows := [][2]string{
		{"OPENFGA_API_URL", "OpenFGA API URL (alias: FGA_API_URL)"},
		{"OPENFGA_STORE_ID", "active store ID (alias: FGA_STORE_ID)"},
		{"OPENFGA_MODEL_ID", "authorization model ID (aliases: OPENFGA_AUTHORIZATION_MODEL_ID, FGA_MODEL_ID, FGA_AUTHORIZATION_MODEL_ID)"},
		{"OPENFGA_API_TOKEN", "API bearer token compatibility fallback; prefer --auth-token-file (alias: FGA_API_TOKEN)"},
		{"OPENFGA_CLIENT_ID", "OAuth client ID for client_credentials (alias: FGA_CLIENT_ID)"},
		{"OPENFGA_CLIENT_SECRET", "OAuth secret compatibility fallback; prefer --auth-client-secret-file (alias: FGA_CLIENT_SECRET)"},
		{"OPENFGA_TOKEN_URL", "OAuth token endpoint for client_credentials (alias: FGA_TOKEN_URL)"},
		{"OPENFGA_API_AUDIENCE", "OAuth audience for client_credentials (alias: FGA_API_AUDIENCE)"},
		{"OPENFGA_SCOPES", "OAuth scopes for client_credentials (alias: FGA_SCOPES)"},
		{"OPENFGA_KEY_FILE", "PEM signing key, private_key_jwt profiles (alias: FGA_KEY_FILE)"},
		{"OPENFGA_PROFILE", "profile to use (alias: FGA_PROFILE)"},
		{"OPENFGA_CONFIG", "path to the config file"},
		{"OPENFGA_ICONS", "icon mode: nerdfont, unicode or off"},
		{"NO_COLOR", "disable colored output"},
		{"CLICOLOR_FORCE / FORCE_COLOR", "force colored output, even through pipes"},
		{"OPENFGA_REDUCED_MOTION", "disable TUI animations (alias: OFGA_REDUCED_MOTION)"},
	}
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	name := lipgloss.NewStyle().Foreground(style.Accent)
	var b strings.Builder
	for _, r := range rows {
		gap := strings.Repeat(" ", width-len(r[0])+3)
		b.WriteString("    " + name.Render(r[0]) + gap + style.Subtitle.Render(r[1]) + "\n")
	}
	return b.String()
}

// blockLine is one line inside a shell block: its text and foreground color.
type blockLine struct {
	text string
	fg   color.Color
}

// exampleLines splits an Example string into block lines: `#` comments are
// dimmed, commands take the normal foreground, blanks stay blank.
func exampleLines(example string) []blockLine {
	ex := strings.TrimRight(example, "\n")
	if ex == "" {
		return nil
	}
	raw := strings.Split(ex, "\n")
	commonIndent := -1
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if commonIndent == -1 || indent < commonIndent {
			commonIndent = indent
		}
	}
	if commonIndent < 0 {
		commonIndent = 0
	}
	var lines []blockLine
	for _, line := range raw {
		line = strings.TrimRight(line, " \t")
		if len(line) >= commonIndent {
			line = line[commonIndent:]
		}
		switch t := strings.TrimSpace(line); {
		case t == "":
			lines = append(lines, blockLine{"", style.Fg})
		case strings.HasPrefix(t, "#"):
			lines = append(lines, blockLine{line, style.Faintc})
		default:
			lines = append(lines, blockLine{line, style.Fg})
		}
	}
	return lines
}

func blockWidth(lines []blockLine) int {
	w := 0
	for _, l := range lines {
		if lw := lipgloss.Width(l.text); lw > w {
			w = lw
		}
	}
	return w + 4 // 2 spaces of horizontal padding each side
}

// shellBlock renders lines as a uniform-width raised terminal block (a subtle
// background with a blank padded row above and below), indented under its
// section heading.
func shellBlock(lines []blockLine, width int) string {
	base := lipgloss.NewStyle().Background(style.BgHighlight)
	blank := base.Width(width).Render("")
	out := []string{"  " + blank}
	for _, l := range lines {
		out = append(out, "  "+base.Foreground(l.fg).Width(width).Padding(0, 2).Render(l.text))
	}
	out = append(out, "  "+blank)
	return strings.Join(out, "\n") + "\n"
}

// sectionHead renders a bold, uppercase section heading.
func sectionHead(title string) string {
	return "\n  " + lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(strings.ToUpper(title)) + "\n\n"
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
	name := lipgloss.NewStyle().Bold(true).Foreground(style.Secondary)
	var b strings.Builder
	for _, sub := range subs {
		gap := strings.Repeat(" ", width-len(sub.Name())+3)
		b.WriteString("    " + name.Render(sub.Name()) + gap + style.Subtitle.Render(sub.Short) + "\n")
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
	name := lipgloss.NewStyle().Foreground(style.Accent)
	var b strings.Builder
	for _, r := range rows {
		gap := strings.Repeat(" ", width-len(r.left)+3)
		b.WriteString("    " + name.Render(r.left) + gap + style.Subtitle.Render(r.help) + "\n")
	}
	return b.String()
}
