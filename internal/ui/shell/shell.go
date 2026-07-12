// Package shell renders the Crush-style playground frame: a left sidebar
// (gradient logo + context + nav + status footer), a main content pane, and a
// bottom status bar, composited flat on a lipgloss canvas with no painted
// panel backgrounds — structure comes from headers and rules. The one boxed
// exception is the centered modal dialog (with dim scrim and drop shadow);
// a bottom-right toast layers on top of everything. Styling is driven by the
// active theme via the style package.
package shell

import (
	"image/color"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	"github.com/sergiught/openfga-cli/internal/ui/logo"
)

const (
	collapseBelow = 76 // total width under which the sidebar hides
	sidebarMin    = 24
	sidebarMax    = 34
	statusHeight  = 1
)

// Focus identifies which region owns the highlight: the sidebar (tab
// selection) lights up its hatch bands + version; the main panel lights up its
// section header. Exactly one is focused at a time.
type Focus int

const (
	FocusSidebar Focus = iota
	FocusPanel
)

// NavItem is one sidebar navigation row.
type NavItem struct {
	Label  string
	Icon   string
	Badge  string
	Active bool
}

// Status describes the segmented bottom bar.
type Status struct {
	Profile string   // filled identity chip, shown first (empty = none)
	Mode    string   // filled keyword chip, e.g. "CHECK" (empty = none)
	Store   string   // raised chip (empty = none)
	Model   string   // raised chip (empty = none)
	Spinner string   // prepended to Left when non-empty
	Left    string   // free status text
	Keys    []string // right-aligned keycaps
}

// Shell holds the current size and the content of each region.
type Shell struct {
	width, height int

	context []string
	nav     []NavItem
	footer  string

	mainTitle string
	mainBody  string

	tagline, version string

	focus Focus

	status Status

	dialogTitle, dialogBody string
	dialogAccent            color.Color // border + title tint (defaults to Primary)
	toast                   string

	drift         float64
	entranceFrac  float64
	entranceGhost bool

	// navTop is the screen row (0-indexed, in the full frame) of the first nav
	// item, recorded during renderSidebar for mouse hit-testing.
	navTop int
	// statusRow and keySpans are the footer's row and per-key column spans,
	// recorded during renderStatus for mouse hit-testing.
	statusRow int
	keySpans  []keySpan
}

type keySpan struct {
	start, end int
	hint       string
}

// KeyHit returns the footer key-hint text at screen cell (x, y), or "" if the
// click didn't land on a footer keycap.
func (s *Shell) KeyHit(x, y int) string {
	if y != s.statusRow {
		return ""
	}
	for _, sp := range s.keySpans {
		if x >= sp.start && x < sp.end {
			return sp.hint
		}
	}
	return ""
}

// NavHit returns the nav index at screen cell (x, y), or -1 if the click didn't
// land on a nav item (sidebar collapsed, click in the panel, or off the list).
func (s *Shell) NavHit(x, y int) int {
	if s.Collapsed() || x >= s.sidebarOccupied() {
		return -1
	}
	idx := y - s.navTop
	if idx < 0 || idx >= len(s.nav) {
		return -1
	}
	return idx
}

// InSidebar reports whether screen column x falls in the sidebar (vs the panel).
func (s *Shell) InSidebar(x int) bool {
	return !s.Collapsed() && x < s.sidebarOccupied()
}

// MainBodyOrigin returns the screen cell (x, y) of the top-left of the main
// pane's body — below the section header and its blank rule line — for mouse
// hit-testing. The main pane has a 1-col left padding and renders as
// "header\n\nbody", so the body starts at row 2.
func (s *Shell) MainBodyOrigin() (int, int) {
	y := 2
	if s.Collapsed() {
		y = 3 // the collapsed tab strip adds one row above the header
	}
	return s.sidebarOccupied() + 1, y
}

// InDialog reports whether screen cell (x, y) is inside the currently-open
// dialog box. It mirrors the centering used when the dialog layer is drawn.
func (s *Shell) InDialog(x, y int) bool {
	if s.dialogTitle == "" && s.dialogBody == "" {
		return false
	}
	dlg := s.renderDialog()
	dw, dh := lipgloss.Width(dlg), lipgloss.Height(dlg)
	dx := (s.width - dw) / 2
	dy := (s.height - dh) / 2
	return x >= dx && x < dx+dw && y >= dy && y < dy+dh
}

// New returns an empty shell.
func New() *Shell { return &Shell{} }

// SetSize records the available terminal size.
func (s *Shell) SetSize(w, h int) { s.width, s.height = w, h }

// Collapsed reports whether the sidebar is hidden at the current width.
func (s *Shell) Collapsed() bool { return s.width < collapseBelow }

// sidebarWidth returns the CONTENT width passed to lipgloss Width().
// The sidebar's total column occupation = sidebarWidth() + 2 (padding) + 1 (border).
func (s *Shell) sidebarWidth() int {
	if s.Collapsed() {
		return 0
	}
	w := s.width / 4
	if w < sidebarMin {
		w = sidebarMin
	}
	if w > sidebarMax {
		w = sidebarMax
	}
	return w
}

// sidebarOccupied returns the column count the sidebar takes up. The sidebar is
// a flat column with no border; Width(w) already includes its padding in
// lipgloss v1. Zero when collapsed.
func (s *Shell) sidebarOccupied() int {
	if s.Collapsed() {
		return 0
	}
	return s.sidebarWidth()
}

func (s *Shell) bodyHeight() int {
	h := s.height - statusHeight
	if h < 1 {
		h = 1
	}
	return h
}

// MainSize returns the drawable interior for main-pane content: the pane is
// flat (1-col margins, header + blank row) — no border rows or columns.
func (s *Shell) MainSize() (int, int) {
	w := s.width - s.sidebarOccupied() - 2 // margins
	if w < 1 {
		w = 1
	}
	h := s.bodyHeight() - 2 // header + blank
	if h < 1 {
		h = 1
	}
	return w, h
}

// SetSidebar sets the sidebar content. The logo is rendered by the shell itself
// (sized to the sidebar width).
func (s *Shell) SetSidebar(context []string, nav []NavItem, footer string) {
	s.context, s.nav, s.footer = context, nav, footer
}

// SetMain sets the main pane title and body.
func (s *Shell) SetMain(title, body string) { s.mainTitle, s.mainBody = title, body }

// SetBrand sets the sidebar's tagline (left) and version (right), rendered
// dim inside the wordmark's hatch band area.
func (s *Shell) SetBrand(tagline, version string) { s.tagline, s.version = tagline, version }

// SetStatus sets the bottom status bar's segments.
func (s *Shell) SetStatus(st Status) { s.status = st }

// SetDialog sets (or clears, when both title and body are empty) the centered
// modal dialog.
func (s *Shell) SetDialog(title, body string, accent ...color.Color) {
	s.dialogTitle, s.dialogBody, s.dialogAccent = title, body, style.Primary
	if len(accent) > 0 && accent[0] != nil {
		s.dialogAccent = accent[0]
	}
}

// SetToast sets (or clears, when empty) the bottom-right toast slot.
func (s *Shell) SetToast(view string) { s.toast = view }

// SetDrift sets the ambient gradient phase for the wordmark and active pill.
func (s *Shell) SetDrift(p float64) { s.drift = p }

// SetFocus sets which region carries the focus highlight (sidebar or panel).
func (s *Shell) SetFocus(f Focus) { s.focus = f }

// SetEntrance drives the launch animation: frac slides the sidebar in from
// the left (1 = fully off-screen, 0 = settled) and ghost dims the main pane.
// frac 0 with ghost false is the steady state.
func (s *Shell) SetEntrance(frac float64, ghost bool) {
	s.entranceFrac, s.entranceGhost = frac, ghost
}

// View composes the full frame on a canvas: a flat sidebar/main/status base
// with no painted surface backgrounds, an optional dimmed scrim + shadowed
// dialog centered on top, and a bottom-right toast layered above everything.
func (s *Shell) View() string {
	body := s.bodyHeight()
	// Safety net: never emit more than height rows or wider than width. Any
	// residual overflow would scroll the terminal and corrupt the layout.
	base := clampFrame(
		lipgloss.JoinVertical(lipgloss.Left, s.composeTop(body), s.renderStatus()),
		s.width, s.height,
	)

	// Layers are collected and drawn through a single Compositor: a bare
	// Layer's X()/Y()/Z() only take effect once the layer hierarchy has been
	// flattened into absolute bounds, which Compositor.Draw does — composing a
	// Layer straight onto a Canvas draws it at the canvas origin regardless of
	// its offset.
	var layers []*lipgloss.Layer
	if s.dialogTitle != "" || s.dialogBody != "" {
		// Dim the base frame into a scrim behind the dialog.
		dim := lipgloss.NewStyle().Foreground(style.Faintc).Render(ansi.Strip(base))
		layers = append(layers, lipgloss.NewLayer(dim).X(0).Y(0).Z(0))

		dlg := s.renderDialog()
		dw, dh := lipgloss.Width(dlg), lipgloss.Height(dlg)
		dx := (s.width - dw) / 2
		dy := (s.height - dh) / 2
		// Shadow: a BgOverlay-filled block offset +1,+1 behind the dialog.
		shadow := lipgloss.NewStyle().Background(style.BgOverlay).
			Width(dw).Height(dh).Render("")
		layers = append(layers,
			lipgloss.NewLayer(shadow).X(dx+1).Y(dy+1).Z(1),
			lipgloss.NewLayer(dlg).X(dx).Y(dy).Z(2),
		)
	} else if s.entranceFrac > 0 && !s.Collapsed() {
		// Entrance: sidebar slides in from the left as its own layer; the
		// main pane and status stay at their final positions.
		off := int(s.entranceFrac * float64(s.sidebarOccupied()))
		rest := lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top,
				strings.Repeat(" ", s.sidebarOccupied()), s.renderMain(body)),
			s.renderStatus(),
		)
		rest = clampFrame(rest, s.width, s.height)
		layers = append(layers,
			lipgloss.NewLayer(rest).X(0).Y(0).Z(0),
			lipgloss.NewLayer(s.renderSidebar(body)).X(-off).Y(0).Z(1),
		)
	} else {
		layers = append(layers, lipgloss.NewLayer(base).X(0).Y(0).Z(0))
	}
	if s.toast != "" {
		tx := s.width - lipgloss.Width(s.toast) - 2
		ty := s.height - lipgloss.Height(s.toast) - 2
		if tx < 0 {
			tx = 0
		}
		if ty < 0 {
			ty = 0
		}
		layers = append(layers, lipgloss.NewLayer(s.toast).X(tx).Y(ty).Z(3))
	}

	cv := lipgloss.NewCanvas(s.width, s.height)
	cv.Compose(lipgloss.NewCompositor(layers...))
	return cv.Render()
}

// composeTop lays out the sidebar (when shown) beside the main pane.
func (s *Shell) composeTop(body int) string {
	main := s.renderMain(body)
	if s.Collapsed() {
		return main
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, s.renderSidebar(body), main)
}

// renderDialog draws the modal box: a rounded, primary-bordered panel with the
// title bold above the body. The interior is flat (base surface) — the active
// form field carries the only highlight; the drop shadow and dimmed scrim give
// the modal its depth.
func (s *Shell) renderDialog() string {
	dw := s.width / 2
	if dw < 36 {
		dw = 36
	}
	if dw > s.width-4 {
		dw = s.width - 4
	}
	accent := s.dialogAccent
	if accent == nil {
		accent = style.Primary
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(s.dialogTitle)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(accent).
		Width(dw).Padding(0, 2).
		Render(title + "\n\n" + s.dialogBody)
}

// DialogSize returns the interior content budget of the modal dialog:
// renderDialog's width math (half the terminal, min 36, max width-4) minus
// its border+padding, and a height that leaves the dialog fully on-screen.
func (s *Shell) DialogSize() (int, int) {
	dw := s.width / 2
	if dw < 36 {
		dw = 36
	}
	if dw > s.width-4 {
		dw = s.width - 4
	}
	w := dw - 6 // border(2) + padding(4)
	if w < 1 {
		w = 1
	}
	h := s.height - 8 // border(2), title+blank(2), hint(1), margins(3)
	if h < 1 {
		h = 1
	}
	return w, h
}

// fitLines truncates every line of s to at most w display columns (ANSI-aware)
// so lipgloss never wraps over-wide content into extra rows.
func fitLines(s string, w int) string {
	if w < 1 {
		w = 1
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "…")
	}
	return strings.Join(lines, "\n")
}

// clampFrame forces s to exactly h lines, each no wider than w columns.
func clampFrame(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if lipgloss.Width(ln) > w {
			lines[i] = ansi.Truncate(ln, w, "")
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (s *Shell) renderSidebar(height int) string {
	w := s.sidebarWidth()
	inner := w - 2
	var b strings.Builder
	// Logo: the stacked OPENFGA block wordmark when the sidebar is wide and
	// tall enough, otherwise a single-line gradient "OpenFGA" with a diagonal
	// field tail (Crush's small-logo treatment).
	// The hatch bands + version are the sidebar's focus indicator: dim when the
	// panel owns focus, lit to bold Primary when the sidebar (tab selection)
	// does.
	frameStyle := lipgloss.NewStyle().Foreground(style.Faintc)
	if s.focus == FocusSidebar {
		frameStyle = lipgloss.NewStyle().Foreground(style.Primary).Bold(true)
	}
	hatch := frameStyle.Render(strings.Repeat("╱", inner))
	hatchDown := frameStyle.Render(strings.Repeat("╲", inner))
	mw, _ := logo.WordmarkSize()
	if inner >= mw && height >= 26 {
		var art string
		if s.entranceFrac > 0 {
			art = logo.Wordmark(1 - s.entranceFrac)
		} else {
			art = logo.Wordmark(-1)
		}
		// Tuck the version onto the wordmark's baseline (the FGA row),
		// right-aligned to the sidebar edge; it shares the frame's focus tint.
		lines := strings.Split(art, "\n")
		last := strings.TrimRight(lines[len(lines)-1], " ")
		ver := frameStyle.Render(s.version)
		gap := inner - lipgloss.Width(last) - lipgloss.Width(ver)
		if gap < 1 {
			gap = 1
		}
		lines[len(lines)-1] = last + strings.Repeat(" ", gap) + ver
		art = strings.Join(lines, "\n")
		// Hatch bands frame the title with one blank line of breathing room on
		// each side; the bottom band mirrors the top with the inverse diagonal.
		b.WriteString(hatch + "\n\n")
		b.WriteString(art + "\n\n")
		b.WriteString(hatchDown + "\n\n")
	} else {
		line := style.Gradient("OpenFGA")
		if rem := inner - lipgloss.Width(line) - 1; rem > 0 {
			line += " " + frameStyle.Render(strings.Repeat("╱", rem))
		}
		b.WriteString(line + "\n")
		b.WriteString(s.brandLine(inner, frameStyle) + "\n\n")
	}
	// The context lines carry the same 1-col horizontal padding as the nav rows
	// so their icon (e.g. the connection dot) aligns with the tab icons.
	for _, line := range s.context {
		b.WriteString(lipgloss.NewStyle().Padding(0, 1).Render(line) + "\n")
	}
	b.WriteString("\n")
	// Record the row where the nav list starts, for mouse hit-testing. The
	// sidebar is the first element in the frame, so this row is also the
	// absolute screen row.
	s.navTop = strings.Count(b.String(), "\n")
	for _, n := range s.nav {
		b.WriteString(s.renderNav(n) + "\n")
	}
	content := b.String()
	footer := s.footer
	gap := height - lipgloss.Height(content) - 1
	if gap > 0 {
		content += strings.Repeat("\n", gap)
	}
	content += footer
	// Truncate each line to the interior width (Width(w) includes the 2 padding
	// cols in lipgloss v1) so long store names/IDs never wrap and push rows down.
	content = fitLines(content, w-2)
	// Cap to the available height: lipgloss's Height() only pads content that's
	// shorter than requested, it never truncates content that's taller (the
	// block wordmark + context + nav can easily exceed a short terminal's
	// budget), which would silently grow the sidebar past `height` and push the
	// status bar off the bottom of the frame.
	if lines := strings.Split(content, "\n"); len(lines) > height {
		content = strings.Join(lines[:height], "\n")
	}

	// Content stays fg-only: no Background() here. The sidebar is a flat
	// column on the shared base background — structure comes from the hatch
	// bands and typography, not a painted panel fill.
	return lipgloss.NewStyle().
		Width(w).Height(height).
		Padding(0, 1).
		Render(content)
}

// brandLine sets the tagline (left) and version (right) in the frame's focus
// tone, space-filled to width — the fallback brand row for the narrow sidebar.
func (s *Shell) brandLine(width int, st lipgloss.Style) string {
	tag := st.Render(s.tagline)
	ver := st.Render(s.version)
	gap := width - lipgloss.Width(tag) - lipgloss.Width(ver)
	if gap < 1 {
		return ansi.Truncate(tag, width, "…")
	}
	return tag + strings.Repeat(" ", gap) + ver
}

func (s *Shell) renderNav(n NavItem) string {
	label := strings.TrimSpace(n.Icon + " " + n.Label)
	if n.Active {
		out := style.GradientPillPhase(label, s.drift)
		if n.Badge != "" {
			out += " " + style.Chip(n.Badge, style.Muted, style.BgHighlight)
		}
		return out
	}
	out := lipgloss.NewStyle().Padding(0, 1).Foreground(style.Muted).Render(label)
	if n.Badge != "" {
		out += " " + style.Chip(n.Badge, style.Muted, style.BgHighlight)
	}
	return out
}

func (s *Shell) renderMain(height int) string {
	// Flat main pane: a section header + hairline rule over left-aligned
	// content, on the shared base background. Structure comes from typography
	// and whitespace; dialogs are the only boxed surface.
	mainTotal := s.width - s.sidebarOccupied()
	if mainTotal < 6 {
		mainTotal = 6
	}
	innerW := mainTotal - 2 // 1-col margin each side
	header := style.SectionHeader(s.mainTitle, innerW)
	if s.focus == FocusPanel {
		title := s.mainTitle
		// Under the mono theme the Primary focus tint collapses to no color, so
		// add a caret marker to keep the focused panel distinguishable.
		if style.Active.Name == "mono" {
			title = "▸ " + title
		}
		header = style.SectionHeaderFocused(title, innerW)
	}
	body := fitLines(s.mainBody, innerW)
	if s.entranceGhost {
		body = lipgloss.NewStyle().Foreground(style.Faintc).Render(ansi.Strip(body))
	}
	content := header + "\n\n" + body
	// When the sidebar is hidden, prepend a compact tab strip so the section
	// context (and the other sections) stay visible.
	if s.Collapsed() {
		content = s.navStrip(innerW) + "\n" + content
	}
	return lipgloss.NewStyle().
		Width(mainTotal).Height(height).
		Padding(0, 1).
		Render(content)
}

// navStrip renders a one-line horizontal tab strip for the collapsed layout:
// the active section as a labeled pill, the rest as dim icons.
func (s *Shell) navStrip(width int) string {
	segs := make([]string, 0, len(s.nav))
	for _, n := range s.nav {
		if n.Active {
			segs = append(segs, style.GradientPillPhase(strings.TrimSpace(n.Icon+" "+n.Label), s.drift))
		} else {
			segs = append(segs, lipgloss.NewStyle().Foreground(style.Muted).Render(n.Icon))
		}
	}
	return ansi.Truncate(strings.Join(segs, " "), width, "…")
}

// capChip wraps a filled chip with powerline end caps when the active icon
// rung provides them; otherwise it returns the plain chip.
func capChip(text string, fg, bg color.Color) string {
	ic := icons.I()
	chip := style.Chip(text, fg, bg)
	if ic.CapL == "" {
		return chip
	}
	capSt := lipgloss.NewStyle().Foreground(bg)
	return capSt.Render(ic.CapL) + chip + capSt.Render(ic.CapR)
}

func (s *Shell) renderStatus() string {
	var segs []string
	if s.status.Profile != "" {
		segs = append(segs, capChip(s.status.Profile, style.OnAccent, style.Primary))
	}
	if s.status.Mode != "" {
		segs = append(segs, capChip(s.status.Mode, style.OnAccent, style.Violet))
	}
	if s.status.Store != "" {
		segs = append(segs, capChip(s.status.Store, style.Fg, style.BgHighlight))
	}
	if s.status.Model != "" {
		segs = append(segs, capChip(s.status.Model, style.Muted, style.BgHighlight))
	}
	left := strings.Join(segs, " ")
	txt := s.status.Left
	if s.status.Spinner != "" {
		txt = s.status.Spinner + " " + txt
	}
	if txt != "" {
		left += " " + lipgloss.NewStyle().Foreground(style.Faintc).Render(txt)
	}
	var keys []string
	for _, k := range s.status.Keys {
		keys = append(keys, style.Keycap(k))
	}
	right := strings.Join(keys, " ")
	rw := lipgloss.Width(right)

	// Record each keycap's column span (right-aligned block) and the footer row
	// for mouse hit-testing.
	s.statusRow = s.bodyHeight()
	s.keySpans = s.keySpans[:0]
	kx := s.width - rw
	for i, k := range s.status.Keys {
		w := lipgloss.Width(keys[i])
		s.keySpans = append(s.keySpans, keySpan{start: kx, end: kx + w, hint: k})
		kx += w + 1 // + separator space
	}
	// Truncate the (possibly long) status text so the bar fits one line and never
	// wraps; keep the right-side key hints visible.
	maxLeft := s.width - rw - 1
	if maxLeft < 0 {
		maxLeft = 0
	}
	left = ansi.Truncate(left, maxLeft, "…")
	gap := s.width - lipgloss.Width(left) - rw
	if gap < 1 {
		gap = 1
	}
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, s.width, "")
}
