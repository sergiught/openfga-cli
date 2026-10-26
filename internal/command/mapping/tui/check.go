package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// checkView lists the findings for the document on screen. The host feeds it;
// it computes nothing itself.
type checkView struct {
	s         *Session
	list      *uilist.List
	findings  []mapping.Finding
	model     string
	modelPath string
	missing   []string
	width     int // the width the rows were last cut for; 0 before the first View
}

func newCheckView(s *Session) *checkView {
	l := uilist.New()
	l.SetCompact(true)
	return &checkView{s: s, list: l}
}

// set replaces the rows. model is the model's label ("" when there is none),
// modelPath the model file in use ("" for a store), missing the types the
// mapping writes that the model lacks.
func (c *checkView) set(fs []mapping.Finding, model, modelPath string, missing []string) {
	c.findings, c.model, c.modelPath, c.missing = fs, model, modelPath, missing
	c.rebuild()
}

// minMessage is how many columns of its message a row keeps when it is cut.
const minMessage = 20

// rebuild lays the rows out for c.width.
func (c *checkView) rebuild() {
	fs := c.findings
	items := make([]uilist.Item, 0, len(fs))
	for i, f := range fs {
		rule, field := "document", ""
		if f.Rule < 0 && f.Field != "" {
			rule = style.SanitizeTerminal(f.Field)
		}
		if f.Rule >= 0 {
			// The field names the subject of mapper's own messages; the
			// section is all that is left of an evaluation finding's.
			rule = c.ruleName(f.Rule)
			if field = style.SanitizeTerminal(mapping.FieldInRule(f.Field)); field == "" {
				field = f.Section
			}
		}
		msg := style.SanitizeTerminal(f.Message)
		text := fitRow(rule, field, msg, c.width)
		items = append(items, uilist.Item{TitleText: text, Filter: text, ID: fmt.Sprintf("finding-%d", i), Index: i})
	}
	c.list.SetItems(items)
}

// fitRow words a row "rule › field — message". When width is known and the row
// would not fit, the field is cut first, from the middle, then the rule name,
// so that the message keeps at least minMessage columns.
func fitRow(rule, field, msg string, width int) string {
	row := func(r, f string) string {
		if f != "" {
			r += " › " + f
		}
		return r + " — " + msg
	}
	if width <= 0 {
		return row(rule, field)
	}
	// The list keeps three columns: the selection bar, its padding, and one more.
	room := width - 3 - 3 - min(lipgloss.Width(msg), minMessage)
	full := lipgloss.Width(rule)
	if field != "" {
		full += 3 + lipgloss.Width(field)
	}
	if full <= room {
		return row(rule, field)
	}
	if field != "" {
		if fr := room - lipgloss.Width(rule) - 3; fr >= 1 {
			return row(rule, middleCut(field, fr))
		}
		field = "…"
		room -= 4
	}
	return row(middleCut(rule, max(room, 1)), field)
}

// middleCut shortens s to w columns with a middle "…", keeping the longer half
// at the start, where a field's name is.
func middleCut(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	// Cut by columns, not runes: a wide character takes two. Truncate drops
	// one its cut would split; TruncateLeft keeps it, so it cuts once more.
	head, tail := w/2, (w-1)/2 // head + tail + 1 = w
	sw := lipgloss.Width(s)
	end := ansi.TruncateLeft(s, sw-tail, "")
	for cut := sw - tail + 1; lipgloss.Width(end) > tail; cut++ {
		end = ansi.TruncateLeft(s, cut, "")
	}
	return ansi.Truncate(s, head, "") + "…" + end
}

func (c *checkView) ruleName(i int) string {
	if r := c.s.rule(i); r != nil {
		if name := style.SanitizeTerminal(r.Name); name != "" {
			return name
		}
	}
	return fmt.Sprintf("rule %d", i+1)
}

// checkBadge is the sidebar badge: how many findings, a tick when the file is
// clean against a model, a dash when there is nothing to check against.
func checkBadge(fs []mapping.Finding, hasModel bool) string {
	switch {
	case len(fs) > 0:
		return fmt.Sprintf("✗ %d", len(fs))
	case hasModel:
		return "✓"
	}
	return "–"
}

func (c *checkView) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, c.list.Update(msg)
	}
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return c, cmd
	}
	switch k.String() {
	case "m":
		c.s.post(doneMsg{result: RequestModel})
	case "enter":
		if it, ok := c.list.Selected(); ok && it.Index < len(c.findings) {
			if f := c.findings[it.Index]; f.Rule >= 0 {
				c.s.post(doneMsg{result: Jump{Rule: f.Rule, Section: f.Section}})
			}
		}
	default:
		return c, c.list.Update(k)
	}
	return c, nil
}

func (c *checkView) View(width, height int) string {
	if width != c.width {
		c.width = width
		c.rebuild()
	}
	var block []string
	if len(c.missing) > 0 {
		// Keep the findings at least a few rows; a short pane trims the type
		// list, never the instruction that fixes it.
		limit := max(height-3, 1)
		for shown := len(c.missing); ; shown-- {
			block = strings.Split(lipgloss.NewStyle().Width(width).Render(c.lacks(shown)), "\n")
			if len(block) <= limit || shown == 0 {
				break
			}
		}
		if len(block) > limit {
			block = block[:limit]
		}
	}
	body := height - len(block)
	if len(block) > 0 {
		body-- // a blank line between
	}
	var top string
	if len(c.findings) == 0 {
		top = windowAround(strings.Split(c.empty(), "\n"), 0, max(body, 1))
	} else {
		c.list.SetSize(width, max(body, 1))
		top = c.list.View()
	}
	if len(block) == 0 {
		return top
	}
	lines := strings.Split(top, "\n")
	if len(lines) > body {
		lines = lines[:max(body, 0)]
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	return strings.Join(append(append(lines, ""), block...), "\n")
}

// lacks words the missing-types block, naming the first shown types.
func (c *checkView) lacks(shown int) string {
	names := make([]string, 0, shown+1)
	for _, m := range c.missing[:shown] {
		names = append(names, style.SanitizeTerminal(m))
	}
	if more := len(c.missing) - shown; more > 0 {
		names = append(names, fmt.Sprintf("… (%d more)", more))
	}
	text := "The model lacks: " + strings.Join(names, ", ") + "."
	if c.modelPath != "" {
		text += " Add them to " + style.SanitizeTerminal(c.modelPath) + ", then push it with 'ofga model write'."
	}
	return text
}

func (c *checkView) empty() string {
	muted := style.Subtitle
	if c.model == "" {
		return muted.Render("– No model: only mapper's checks ran (m to choose one).")
	}
	return "✓ No problems.\n" + muted.Render("mapper accepts the mapping\n"+
		"every type and relation it names is in "+style.SanitizeTerminal(c.model))
}

func (c *checkView) Keys() []string {
	keys := []string{"m model"}
	if len(c.findings) > 0 {
		keys = append([]string{"↵ open", "/ filter"}, keys...)
	}
	return keys
}

func (c *checkView) heading() (string, string) {
	if c.model == "" {
		return "Check", "No model · m load"
	}
	return "Check", style.SanitizeTerminal(c.model) + " · m change"
}

func (c *checkView) help() (string, string) {
	return "Check", "Everything wrong with the mapping as it stands, and the model types it " +
		"writes that the model lacks. ↵ opens the rule a finding belongs to."
}

func (c *checkView) filtering() bool { return c.list.SettingFilter() }
