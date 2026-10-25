package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

const (
	minWidth, minHeight = 60, 16
	// previewMin is the main width from which the preview sits beside the
	// Rules stack. Below it the editor needs every column.
	previewMin    = 100
	previewGutter = 2
	// noModel is the model label while none is chosen.
	noModel = "no model"
)

var paneNames = [paneCount]string{"Rules", "Check", "Tests", "Apply"}

func (w *workspace) View() string {
	if w.width < minWidth || w.height < minHeight {
		return lipgloss.Place(w.width, w.height, lipgloss.Center, lipgloss.Center,
			style.Faint.Render("terminal too small — need at least 60×16"))
	}
	w.sh.SetSidebar(w.sidebarContext(), w.nav(), "")
	w.sh.SetBrand("", w.opts.Version)
	w.sh.SetMain(w.mainTitle(), w.mainBody())
	w.sh.SetStatus(w.status())
	w.setDialog()
	w.sh.SetToast(w.toasts.View())
	w.sh.SetFocus(w.focus)
	return w.sh.View()
}

// mainSize is the room the active pane has under its header.
func (w *workspace) mainSize() (int, int) {
	mw, mh := w.sh.MainSize()
	if w.sh.Collapsed() {
		mh-- // the shell draws its nav strip above the header
	}
	return mw, max(mh, 1)
}

// previewShown is whether the Rules pane has room for the preview beside its
// stack. A dialog on top is a question about the stack, so it has the pane.
func (w *workspace) previewShown() bool {
	mw, _ := w.mainSize()
	return mw >= previewMin && !w.stacks[paneRules].Dialog()
}

// mainTitle is the breadcrumb of what is open in the active pane, after a dot
// when there are unsaved edits. The status bar says so too, but it cuts its
// left side first, and on a narrow terminal the keys leave it no room.
func (w *workspace) mainTitle() string {
	title := paneNames[w.active]
	if titles := w.stacks[w.active].Titles(); len(titles) > 0 {
		title = strings.Join(titles, " › ")
	}
	if w.dirty {
		title = "● " + title
	}
	return title
}

func (w *workspace) mainBody() string {
	mw, mh := w.mainSize()
	if w.active == paneRules && w.previewShown() {
		ew := min(max(mw/2-2, 32), 72)
		editor := lipgloss.NewStyle().Width(ew + previewGutter).Render(w.paneBody(paneRules, ew, mh))
		return fitHeight(lipgloss.JoinHorizontal(lipgloss.Top,
			editor, w.preview.View(mw-ew-previewGutter, mh)), mh)
	}
	return fitHeight(w.paneBody(w.active, mw, mh), mh)
}

// paneBody is pane p's top component under its subtitle.
func (w *workspace) paneBody(p pane, width, height int) string {
	if p == paneApply {
		if why := w.applyBlocked(); why != "" {
			return style.Subtitle.Width(width).Render(why)
		}
	} else if w.loadErr != nil && p != paneCheck {
		return style.Subtitle.Width(width).Render(mustLoad)
	}
	st := w.stacks[p]
	var head string
	if banner := w.readOnlyBanner(width); banner != "" {
		head = banner + "\n\n"
	}
	if _, sub := st.Heading(); sub != "" {
		head += style.Subtitle.Width(width).Render(sub) + "\n\n"
	}
	if p == paneApply && w.dirty {
		head += style.Warn.Width(width).Render(applyUnsaved) + "\n\n"
	}
	// head's trailing newline starts the row the component begins on.
	rows := max(height-lipgloss.Height(head)+1, 1)
	return head + st.View(width, rows)
}

// fitHeight cuts s to h lines. The shell pads the main pane but never cuts it,
// and a pane taller than its room would push the status bar off the screen.
func fitHeight(s string, h int) string {
	if lines := strings.Split(s, "\n"); len(lines) > h {
		return strings.Join(lines[:h], "\n")
	}
	return s
}

func (w *workspace) sidebarContext() []string {
	// The sidebar's columns less its padding and the context line's own.
	mw, _ := w.sh.MainSize()
	room := max(w.width-mw-2-4, 8)
	return []string{
		style.Value.Render(pathTail(style.SanitizeTerminal(w.path), room)),
		style.Faint.Render(pathTail(style.SanitizeTerminal(w.model.label), room)),
	}
}

func (w *workspace) nav() []shell.NavItem {
	ic := icons.I()
	// Not ic.Check for Check: a tick beside the name would read as its verdict.
	glyphs := [paneCount]string{ic.Tuple, ic.Query, ic.Assert, ic.Change}
	badges := [paneCount]string{
		strconv.Itoa(len(w.doc.Rules)),
		checkBadge(w.findings, w.sess.Index != nil),
		w.testsBadge(),
		w.applyBadge(),
	}
	items := make([]shell.NavItem, paneCount)
	for p := range paneCount {
		items[p] = shell.NavItem{
			Label: paneNames[p], Icon: glyphs[p], Badge: badges[p],
			Key: strconv.Itoa(int(p) + 1), Active: p == w.active,
		}
	}
	return items
}

// testsBadge is passed/total, or a dash until a run has something to count.
func (w *workspace) testsBadge() string {
	if !w.tests.Compiled {
		return "–"
	}
	passed := 0
	for _, o := range w.tests.Outcomes {
		if o.Passed {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d", passed, len(w.tests.Outcomes))
}

func (w *workspace) status() shell.Status {
	st := shell.Status{Model: style.SanitizeTerminal(w.model.label), Left: w.statusLeft(), Keys: w.keys()}
	if w.busy > 0 {
		st.Spinner = w.spin.View()
	}
	return st
}

func (w *workspace) statusLeft() string {
	var parts []string
	if w.dirty {
		parts = append(parts, "● unsaved")
	}
	if n := len(w.findings); n > 0 {
		parts = append(parts, mapping.Plural(n, "problem"))
	}
	return strings.Join(parts, " · ")
}

// keys are the hints for what has the keys now.
func (w *workspace) keys() []string {
	switch {
	case w.help:
		return []string{"↑↓ scroll", "esc close"}
	case w.overlay != nil:
		return w.fitKeys(slices.Clone(w.overlay.Keys()))
	case w.focus == shell.FocusSidebar:
		return []string{"↑↓ move", "↵ open", "q quit", "? help"}
	}
	if w.loadErr != nil {
		if w.active == paneCheck {
			return []string{"e editor", "/ filter", "? help"}
		}
		return []string{"e editor", "? help"}
	}
	if w.paneHidden() {
		return []string{"? help"}
	}
	st := w.stacks[w.active]
	keys := slices.Clone(st.Keys())
	// While a field takes text, esc and ? are the field's.
	if st.Typing() {
		return w.fitKeys(keys)
	}
	// esc at the top of a pane is the workspace's, whatever the root calls it
	// (the rules hub says quit), except while Apply plans: then it cancels.
	if st.Depth() == 1 && !w.planningShown() {
		for i, k := range keys {
			if strings.HasPrefix(k, "esc ") {
				keys[i] = "esc sidebar"
			}
		}
	}
	if !slices.Contains(keys, "? help") {
		keys = append(keys, "? help")
	}
	return w.fitKeys(keys)
}

const (
	// maxChipRoom is the most the model chip is allowed to claim from the keys:
	// a longer label is cut by the shell.
	maxChipRoom = 14
	// chipChrome is what the chip costs besides its label: padding, caps, a gap.
	chipChrome = 8
)

// fitKeys drops whole hints from the end until the keycaps leave room for the
// model chip, so the status bar never cuts one mid-word. "? help" and the esc
// hints go last: they are how the user finds the rest and gets out.
func (w *workspace) fitKeys(keys []string) []string {
	room := w.width - min(lipgloss.Width(w.model.label), maxChipRoom) - chipChrome
	width := func() int {
		n := 0
		for _, k := range keys {
			n += lipgloss.Width(style.Keycap(k)) + 1
		}
		return n
	}
	for i := len(keys) - 1; i >= 0 && width() > room; i-- {
		if keys[i] != "? help" && !strings.HasPrefix(keys[i], "esc ") {
			keys = slices.Delete(keys, i, i+1)
		}
	}
	// Still too wide: the esc hints are given up too, the last one standing.
	for i := len(keys) - 1; i > 0 && width() > room; i-- {
		if keys[i] != "? help" {
			keys = slices.Delete(keys, i, i+1)
		}
	}
	return keys
}

func (w *workspace) setDialog() {
	dw, dh := w.sh.DialogSize()
	switch {
	case w.help:
		w.sh.SetDialog("Keys", w.helpBody(dw, dh))
	case w.overlay != nil:
		title, _ := w.overlay.Heading()
		body := w.overlayBody(dw, dh)
		if w.overlayAlarms() {
			w.sh.SetDialog(title, body, style.Red)
		} else {
			w.sh.SetDialog(title, body)
		}
	default:
		w.sh.SetDialog("", "")
	}
}

// overlayAlarms is whether the overlay asks about losing something, which the
// dialog marks in red.
func (w *workspace) overlayAlarms() bool {
	switch w.overlay.top().(type) {
	case *quitPrompt, *confirm:
		return true
	}
	return false
}

// overlayBody is the overlay's subtitle, its top component, and its keys.
// DialogSize's height leaves a row for the keys line, so the component gets
// the rest less the blank row above that line.
func (w *workspace) overlayBody(dw, dh int) string {
	st := w.overlay
	var head string
	if _, sub := st.Heading(); sub != "" {
		head = style.Subtitle.Width(dw).Render(sub) + "\n\n"
	}
	rows := max(dh-lipgloss.Height(head), 1)
	return head + st.View(dw, rows) + "\n\n" + keysLine(st.Keys(), dw)
}

// keysLine is the hints on one line. A hint that does not fit is dropped whole,
// last first, rather than cut mid-word; the first is cut only when alone.
func keysLine(keys []string, width int) string {
	for len(keys) > 1 && lipgloss.Width(strings.Join(keys, " · ")) > width {
		keys = keys[:len(keys)-1]
	}
	return style.Faint.Render(ansi.Truncate(strings.Join(keys, " · "), width, "…"))
}

// helpBody is the global keys, then what the focused component explains,
// scrolled to helpOff.
func (w *workspace) helpBody(dw, dh int) string {
	body := style.Faint.Render("GLOBAL") + "\n" + keyTable(globalKeys, dw)
	st := w.stacks[w.active]
	if w.overlay != nil {
		st = w.overlay
	}
	if title, text, ok := st.Help(); ok {
		body += "\n\n" + style.Faint.Render(strings.ToUpper(title)) + "\n" + text
	}
	// Measured as the dialog will wrap it, or the scroll would count rows that
	// are not the ones on screen.
	lines := strings.Split(lipgloss.NewStyle().Width(dw).Render(body), "\n")
	rows := max(dh-1, 1)
	w.helpOff = min(w.helpOff, max(len(lines)-rows, 0))
	hint := []string{"? / esc / q / ↵ close"}
	if len(lines) > rows {
		hint = append([]string{"↑↓ scroll"}, hint...)
	}
	if lipgloss.Width(strings.Join(hint, " · ")) > dw {
		hint[len(hint)-1] = "esc close"
	}
	return scrolled(lines, w.helpOff, rows) + "\n\n" + keysLine(hint, dw)
}

// scrolled is h lines of lines from off, a marker replacing the first or last
// line when there is more that way.
func scrolled(lines []string, off, h int) string {
	lines = lines[off:]
	if off > 0 {
		lines[0] = style.Faint.Render(fmt.Sprintf("⋯ %d above", off+1))
	}
	if len(lines) > h {
		hidden := len(lines) - h + 1
		lines = lines[:h]
		lines[h-1] = style.Faint.Render(fmt.Sprintf("⋯ %d more", hidden))
	}
	return strings.Join(lines, "\n")
}

// keyTable is rows of key and what it does, the keys in a column and each
// description wrapped beside its key.
func keyTable(rows [][2]string, width int) string {
	kw := 0
	for _, r := range rows {
		kw = max(kw, lipgloss.Width(r[0]))
	}
	indent := strings.Repeat(" ", kw+2)
	wrap := lipgloss.NewStyle().Width(max(width-kw-2, 8))
	var out []string
	for _, r := range rows {
		for i, line := range strings.Split(wrap.Render(r[1]), "\n") {
			lead := indent
			if i == 0 {
				lead = style.Key.Render(r[0]) + strings.Repeat(" ", kw-lipgloss.Width(r[0])+2)
			}
			out = append(out, lead+style.Subtitle.Render(line))
		}
	}
	return strings.Join(out, "\n")
}
