package base

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/agentskill"
)

var (
	flagTokenRE = regexp.MustCompile(`--[a-z][a-z0-9-]*`)
	wordRE      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	spanRE      = regexp.MustCompile("`([^`\n]+)`")
)

// skillSpans returns every command-like span of body: each line of a fenced
// block and each inline backtick span, cut at shell separators.
func skillSpans(body string) []string {
	var raw []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") {
				raw = append(raw, t)
			}
			continue
		}
		for _, m := range spanRE.FindAllStringSubmatch(line, -1) {
			raw = append(raw, m[1])
		}
	}
	var spans []string
	for _, r := range raw {
		for _, sep := range []string{"|", "&&", ";"} {
			r, _, _ = strings.Cut(r, sep)
		}
		spans = append(spans, r)
	}
	return spans
}

func subcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

func hasFlag(cmd *cobra.Command, name string) bool {
	// cobra adds --help lazily, at execution time.
	return name == "help" || cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil
}

// scanSkill reports every command path or flag in body that does not exist in
// root's tree.
func scanSkill(root *cobra.Command, body string) []string {
	var misses []string
	known := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(root)
	known["help"] = true

	for _, span := range skillSpans(body) {
		toks := strings.Fields(span)
		for i, tok := range toks {
			if tok != "ofga" {
				continue
			}
			cmd := root
			rest := toks[i+1:]
			for len(rest) > 0 && wordRE.MatchString(rest[0]) {
				if sub := subcommand(cmd, rest[0]); sub != nil {
					cmd, rest = sub, rest[1:]
					continue
				}
				if cmd.HasSubCommands() {
					misses = append(misses, "unknown command in `"+span+"`: "+cmd.CommandPath()+" "+rest[0])
				}
				break
			}
			for _, a := range rest {
				if a == "ofga" {
					break
				}
				if !strings.HasPrefix(a, "--") || a == "--" {
					continue
				}
				name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
				if !hasFlag(cmd, name) {
					misses = append(misses, "unknown flag "+a+" on `"+cmd.CommandPath()+"` in `"+span+"`")
				}
			}
		}
	}
	for _, f := range flagTokenRE.FindAllString(body, -1) {
		if !known[strings.TrimPrefix(f, "--")] {
			misses = append(misses, "flag "+f+" exists nowhere in the tree")
		}
	}
	return misses
}

func TestSkillMentionsOnlyRealCommandsAndFlags(t *testing.T) {
	root := metaRoot(t).Command()
	if misses := scanSkill(root, string(agentskill.Content())); len(misses) > 0 {
		t.Fatalf("SKILL.md names things ofga does not have:\n  %s", strings.Join(misses, "\n  "))
	}
}

func TestScanSkillCatchesBadTokens(t *testing.T) {
	root := metaRoot(t).Command()
	body := "run `ofga storez list` or `ofga stores list --nope`\n```\nofga tuples write a b c --dry-run --bogus=1\nofga stores list --json | jq .\n```\nbare --ghost here\n"
	got := strings.Join(scanSkill(root, body), "\n")
	for _, want := range []string{"ofga storez", "unknown flag --nope", "unknown flag --bogus=1", "flag --ghost exists nowhere"} {
		if !strings.Contains(got, want) {
			t.Errorf("misses missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unknown flag --dry-run") || strings.Contains(got, "unknown flag --json") {
		t.Errorf("valid flags reported:\n%s", got)
	}
}
