package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// previewMode is which of the pane's two long sections is on show. They take
// turns: both grow, and a fixed split between two growing things starves both.
type previewMode int

const (
	previewDoc previewMode = iota
	previewPayload
)

// Preview is the pane beside the editor: the file being built or the sample's
// payload, over what the sample evaluates to. A host draws it beside its
// stack, not on it, so its keys reach it whatever editor is on top.
type Preview struct {
	s     *Session
	title string // heads the file section: the path the mapping saves to
	at    Focus
	p     mapping.Preview

	// label and payload are the focused sample as the pane shows it, worked
	// out once per Refresh: inside an iterator that means asking mapper.
	label, payload string

	mode         previewMode
	docOff       int
	payloadOff   int
	maxOff       int // how far the section on show scrolls, measured by View
	page         int // the rows that section had at the last View
	payloadShown string
}

// NewPreview builds the pane. title heads the file section.
func NewPreview(s *Session, title string) *Preview { return &Preview{s: s, title: title} }

// Refresh re-evaluates the document against the sample of the rule at points
// at. Evaluating a mapping-sized document costs microseconds and is capped by
// mapping.EvalTimeout, so a host calls it after every edit.
func (c *Preview) Refresh(at Focus) {
	c.at = at
	var sample map[string]any
	if r := c.s.rule(at.Rule); r != nil && r.Sample != nil {
		sample = r.Sample.Event
	}
	c.p = mapping.Evaluate(c.s.Ctx, c.s.Doc, sample)
	c.label, c.payload = c.samplePayload()
}

// Result is the evaluation the pane is showing.
func (c *Preview) Result() mapping.Preview { return c.p }

// Update takes the pane's own keys: ctrl+t switches section, pgup/pgdn page
// the one on show. Everything else is ignored.
func (c *Preview) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "ctrl+t":
		if c.mode == previewPayload {
			c.mode = previewDoc
		} else if c.payload != "" {
			c.mode = previewPayload
		}
	case "pgup", "pgdown":
		n := 1
		if k.String() == "pgup" {
			n = -1
		}
		// A page is one row short of the window, so the line the eye stopped
		// on is still there after the jump.
		off := c.offset()
		*off = min(max(*off+n*max(c.page-1, 1), 0), c.maxOff)
	}
	return c, nil
}

// Keys offers the switch, when there is a section to switch to.
func (c *Preview) Keys() []string {
	if label, ok := c.Switch(); ok {
		return []string{"ctrl+t " + label}
	}
	return nil
}

// Switch is what ctrl+t leads to, in the words the section header uses: the
// file from the payload, the payload from the file, nothing without a sample.
func (c *Preview) Switch() (string, bool) {
	if c.showsPayload() {
		return "file", true
	}
	if c.payload == "" {
		return "", false
	}
	return c.label, true
}

// showsPayload is whether the payload is on show. The mode is a preference: a
// rule with no sample shows the file whatever the mode says.
func (c *Preview) showsPayload() bool { return c.mode == previewPayload && c.payload != "" }

// offset is the scroll offset of the section on show. The two keep their own,
// so a look at the payload and back does not lose the user's place in the file.
func (c *Preview) offset() *int {
	if c.showsPayload() {
		return &c.payloadOff
	}
	return &c.docOff
}

// View draws one long section, then the evaluation pinned under it.
func (c *Preview) View(w, rows int) string {
	if w < 20 {
		return ""
	}
	eval := c.evaluationLines(w)
	// A different payload starts at the top: an offset carried across would
	// open the next rule's event somewhere in its middle.
	if c.payload != c.payloadShown {
		c.payloadShown, c.payloadOff = c.payload, 0
	}
	// A long path gives way from the front, keeping the file's name, and to
	// half the header, leaving the other half for the notes.
	title, body := pathTail(c.title, w/2), yamlRows(string(c.p.YAML), w)
	if c.showsPayload() {
		title, body = c.label, jsonRows(c.payload, w)
	}
	other, _ := c.Switch()
	window, notes := c.window(body, rows-lipgloss.Height(eval)-3, other)

	var b strings.Builder
	if window != "" {
		b.WriteString(style.SectionHeaderNoted(title, w, notes...))
		b.WriteString("\n")
		b.WriteString(window)
		b.WriteString("\n\n")
	}
	b.WriteString(style.SectionHeader("preview", w))
	b.WriteString("\n")
	b.WriteString(eval)
	return b.String()
}

// window is the slice of the section scrolled to, and the notes its header may
// carry, longest first. The ceiling and the page are recorded here because
// only the render knows how many rows the section came to.
func (c *Preview) window(rows []string, n int, other string) (string, []string) {
	c.page, c.maxOff = n, 0
	if n < 1 || len(rows) == 0 {
		return "", nil
	}
	off := c.offset()
	c.maxOff = max(len(rows)-n, 0)
	*off = min(*off, c.maxOff)

	var switchNote string
	if other != "" {
		switchNote = "ctrl+t " + other
	}
	if c.maxOff == 0 {
		return strings.Join(rows, "\n"), []string{switchNote}
	}
	pos := fmt.Sprintf("pgup/pgdn %d-%d/%d", *off+1, *off+n, len(rows))
	notes := []string{pos}
	if switchNote != "" {
		notes = []string{pos + " · " + switchNote, switchNote, pos}
	}
	return strings.Join(rows[*off:*off+n], "\n"), notes
}

// samplePayload is the focused rule's sample as indented JSON, and the name to
// head it with: the root the expressions on screen are written against.
// Inside an iterator that is the first item under the alias, as mapper itself
// evaluates the source.
func (c *Preview) samplePayload() (string, string) {
	r := c.s.rule(c.at.Rule)
	if r == nil || r.Sample == nil {
		return "", ""
	}
	label, value := "input payload", any(r.Sample.Event)
	if c.at.InIter && r.Iterator != nil && r.Iterator.As != "" {
		item, ok := mapping.IteratorItem(c.s.Ctx, *r, r.Sample.Event)
		if !ok {
			return "", ""
		}
		label, value = r.Iterator.As+" payload", item
	}
	out, _ := json.MarshalIndent(value, "", "  ")
	return label, string(out)
}

// diagLocation prefixes a diagnostic with its line, or with nothing when mapper
// could not place it: "line 0" is a place no file has.
func diagLocation(d mapper.Diagnostic) string {
	if d.Position.StartLine < 1 {
		return ""
	}
	return fmt.Sprintf("line %d: ", d.Position.StartLine)
}

func (c *Preview) evaluationLines(w int) string {
	// An empty document's only diagnostic is that it has no rules yet, which
	// is the thing the screen beside it is inviting the user to fix.
	if len(c.s.Doc.Rules) == 0 {
		return style.Faint.Render(wrapText("tuples appear here as you add rules", w))
	}
	var out []string
	guided := false
	for _, d := range c.p.Diagnostics {
		if c.isGuidedDiagnostic(d) {
			if !guided {
				out = append(out, style.Subtitle.Render(wrapText(newRuleGuidance, w)))
			}
			guided = true
			continue
		}
		line := Glyph(icons.I().Cross, diagLocation(d)+d.Message)
		if d.Field != "" {
			line = Glyph(icons.I().Cross, diagLocation(d)+d.Field+": "+d.Message)
		}
		out = append(out, style.Failure.Render(wrapText(sanitizeKeepingLines(line), w)))
	}
	if c.p.EvalErr != nil {
		out = append(out, style.Failure.Render(
			wrapText(sanitizeKeepingLines(Glyph(icons.I().Cross, c.p.EvalErr.Error())), w)))
	}
	for _, t := range c.p.Tuples {
		action := string(t.Action)
		if action == "" {
			action = "write"
		}
		// An evaluated tuple is built from the user's own event payload.
		text := fmt.Sprintf("%-6s %s  %s  %s", action, t.User, t.Relation, t.Object)
		// A tuple FGA would refuse is not a good one, however cleanly it
		// evaluated.
		if err := mapping.CheckTuple(t); err != nil {
			out = append(out, style.Failure.Render(wrapText(style.SanitizeTerminal(
				Glyph(icons.I().Cross, text+" — "+err.Error())), w)))
			continue
		}
		out = append(out, style.Success.Render(
			wrapText(style.SanitizeTerminal(Glyph(icons.I().Check, text)), w)))
	}
	for _, op := range c.p.Filters {
		for _, f := range op.Filters {
			out = append(out, lipgloss.NewStyle().Foreground(style.Primary).Render(
				wrapText(style.SanitizeTerminal(Glyph(icons.I().Change,
					fmt.Sprintf("%-6s %s", f.Action, filterSummary(f)))), w)))
		}
	}
	for _, r := range c.p.Rules {
		if r.Status == "skipped" {
			out = append(out, style.Faint.Render(
				wrapText(style.SanitizeTerminal(fmt.Sprintf("– %s: skipped", r.Name)), w)))
		}
	}
	// Gated on the sample having run cleanly: "0 of 40" over a compile error
	// would read as a measurement rather than as the absence of one, and a
	// sample mapper fails produces no tuples, whatever it built before failing.
	// Over budget is the one failure the line itself explains.
	if c.p.Evaluated && (c.p.OK() || c.p.OverBudget) {
		out = append(out, budgetLine(c.p, w))
	}
	if len(out) == 0 {
		return style.Faint.Render("no sample event yet")
	}
	return strings.Join(out, "\n")
}

// isGuidedDiagnostic reports whether d is mapper's "must contain at least one
// tuple" about a rule that is still new, which the pane words as guidance.
func (c *Preview) isGuidedDiagnostic(d mapper.Diagnostic) bool {
	if d.Message != "must contain at least one tuple" {
		return false
	}
	for i := range c.s.Doc.Rules {
		r := &c.s.Doc.Rules[i]
		ref := fmt.Sprintf("rules[%d].", i)
		if r.Name != "" {
			ref = fmt.Sprintf("rules[%q].", r.Name)
		}
		if isGuided(r) && strings.HasPrefix(d.Field, ref) {
			return true
		}
	}
	return false
}

// budgetLine is the sample's tuple count against mapper's per-event cap. Over
// the cap mapper fails the whole event and reports no tuples at all, so the
// count cannot say it; OverBudget does, and the line turns red. The cap is
// mapper's default, which every ofga command uses; a service running the
// mapping can configure its own, so the line says whose number it is.
func budgetLine(p mapping.Preview, w int) string {
	if p.OverBudget {
		return lipgloss.NewStyle().Foreground(style.Red).Render(wrapText(fmt.Sprintf(
			"more than %d tuples — mapper fails the whole event", mapping.MaxTuples), w))
	}
	return lipgloss.NewStyle().Foreground(style.Faintc).Render(wrapText(fmt.Sprintf(
		"%d of %d tuples per event (mapper's default limit)", p.TupleCount(), mapping.MaxTuples), w))
}

func filterSummary(f language.TupleFilter) string {
	var parts []string
	for _, p := range [][2]string{{"user", f.User}, {"relation", f.Relation}, {"object", f.Object}} {
		if p[1] != "" {
			parts = append(parts, p[0]+"="+p[1])
		}
	}
	return strings.Join(parts, " ")
}

// sanitizeKeepingLines strips terminal controls per line, so mapper's
// multi-line errors keep the caret art pointing at the offending column.
func sanitizeKeepingLines(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = style.SanitizeTerminal(lines[i])
	}
	return strings.Join(lines, "\n")
}
