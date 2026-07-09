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

// NavItem is one sidebar navigation row.
type NavItem struct {
	Label  string
	Icon   string
	Badge  string
	Active bool
}

// Status describes the segmented bottom bar.
type Status struct {
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

	status Status

	dialogTitle, dialogBody string
	toast                   string

	drift         float64
	entranceFrac  float64
	entranceGhost bool
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
func (s *Shell) SetDialog(title, body string) { s.dialogTitle, s.dialogBody = title, body }

// SetToast sets (or clears, when empty) the bottom-right toast slot.
func (s *Shell) SetToast(view string) { s.toast = view }

// SetDrift sets the ambient gradient phase for the wordmark and active pill.
func (s *Shell) SetDrift(p float64) { s.drift = p }

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
		dx := (s.width - lipgloss.Width(dlg)) / 2
		dy := (s.height - lipgloss.Height(dlg)) / 2
		// Shadow: a BgOverlay-filled block offset +1,+1 behind the dialog.
		shadow := lipgloss.NewStyle().Background(style.BgOverlay).
			Width(lipgloss.Width(dlg)).Height(lipgloss.Height(dlg)).Render("")
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

// renderDialog draws the modal box: a rounded, primary-bordered panel on the
// raised surface, with the title centered-bold and the body below it.
func (s *Shell) renderDialog() string {
	dw := s.width / 2
	if dw < 36 {
		dw = 36
	}
	if dw > s.width-4 {
		dw = s.width - 4
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(style.Violet).Render(s.dialogTitle)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(style.Violet).
		Background(style.BgRaised).
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
	// Logo: the real OpenFGA mark when the sidebar is wide and tall enough,
	// otherwise a compact wordmark with a diagonal field tail (Crush's
	// small-logo treatment).
	hatch := lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Repeat("╱", inner))
	mw, _ := logo.MarkSize()
	if inner >= mw && height >= 26 {
		var art string
		if s.entranceFrac > 0 {
			art = logo.MarkShimmer(1 - s.entranceFrac)
		} else {
			art = logo.Mark()
		}
		b.WriteString(hatch + "\n")
		b.WriteString(s.brandLine(inner) + "\n")
		b.WriteString(art + "\n")
		b.WriteString(hatch + "\n\n")
	} else {
		line := style.Gradient("ofga")
		if rem := inner - lipgloss.Width(line) - 1; rem > 0 {
			line += " " + lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Repeat("╱", rem))
		}
		b.WriteString(line + "\n")
		b.WriteString(s.brandLine(inner) + "\n\n")
	}
	for _, line := range s.context {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
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

// brandLine sets the tagline (left) and version (right) in the dim tone,
// space-filled to width — the context row inside the wordmark's hatch band.
func (s *Shell) brandLine(width int) string {
	tag := lipgloss.NewStyle().Foreground(style.Faintc).Render(s.tagline)
	ver := lipgloss.NewStyle().Foreground(style.Faintc).Render(s.version)
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
	body := fitLines(s.mainBody, innerW)
	if s.entranceGhost {
		body = lipgloss.NewStyle().Foreground(style.Faintc).Render(ansi.Strip(body))
	}
	return lipgloss.NewStyle().
		Width(mainTotal).Height(height).
		Padding(0, 1).
		Render(header + "\n\n" + body)
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
