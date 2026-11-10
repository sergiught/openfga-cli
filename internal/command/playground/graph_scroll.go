package playground

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/fga"
)

const (
	// graphFPS is the animation refresh rate for spring-based graph scrolling.
	graphFPS = 60
	// graphLineStep is how many lines a single arrow keypress targets.
	graphLineStep = 3
	// graphColStep is how many columns a single left/right keypress pans.
	graphColStep = 6
)

// graphTickMsg drives the graph scroll animation, one frame at a time.
type graphTickMsg time.Time

func graphTick() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(graphFPS), func(t time.Time) tea.Msg {
		return graphTickMsg(t)
	})
}

// graphOpts is the diagram state handed to the renderer.
func (m *Model) graphOpts() fga.DiagramOptions {
	return fga.DiagramOptions{Expanded: m.graphExpanded, Selected: m.graphSel, ViewportHeight: m.graphBodyHeight(), FocusEdges: m.graphFocusEdges}
}

// renderGraph renders the model pane in the active view: the node-link
// diagram (default) or the weighted graph (toggled with `v`). The diagram path
// records card bounds for selection.
func (m *Model) renderGraph() string {
	if m.graphView == 1 {
		m.graphBounds = nil
		return m.pinGraphHeader(m.graph.RenderWeightedDiagram())
	}
	h := m.graphBodyHeight()
	out, bounds := m.graph.RenderDiagramCached(m.graphCache, m.graphOpts())
	m.graphBounds = bounds
	body := m.pinGraphHeader(out)
	if m.graphBodyHeight() != h {
		// The legend wrapped onto a different number of lines than the
		// layout assumed (first render, resize, focus hint toggled): lay out
		// again for the height actually left. The legend doesn't depend on
		// the height, so once is enough.
		out, m.graphBounds = m.graph.RenderDiagramCached(m.graphCache, m.graphOpts())
		body = m.pinGraphHeader(out)
	}
	return body
}

// pinGraphHeader splits the legend (everything before the first blank line)
// off a rendered graph and returns the rest for the viewport. The legend is
// wrapped to the pane and drawn above the viewport, which shrinks to make
// room; card bounds shift up so they stay relative to the viewport content.
func (m *Model) pinGraphHeader(out string) string {
	header, body, ok := strings.Cut(out, "\n\n")
	if !ok {
		m.graphHeader = ""
		m.graphVP.SetHeight(m.graphBodyHeight())
		return out
	}
	removed := strings.Count(header, "\n") + 2
	for i := range m.graphBounds {
		m.graphBounds[i].Y -= removed
	}
	w, _ := m.contentSize()
	m.graphHeader = ansi.Wrap(header, w, "")
	m.graphVP.SetHeight(m.graphBodyHeight())
	return body
}

// graphHeaderHeight is the lines the pinned legend takes, its blank
// separator line included.
func (m *Model) graphHeaderHeight() int {
	if m.graphHeader == "" {
		return 0
	}
	return strings.Count(m.graphHeader, "\n") + 2
}

// graphBodyHeight is the pane height left for the graph viewport below the
// pinned legend.
func (m *Model) graphBodyHeight() int {
	_, h := m.contentSize()
	return max(1, h-m.graphHeaderHeight())
}

// graphIsSmall reports whether a model is small enough to open fully expanded.
func graphIsSmall(g fga.Graph) bool {
	rels := 0
	for _, t := range g.Types {
		rels += len(t.Relations)
	}
	return len(g.Types) <= 8 && rels <= 40
}

// initGraphState resets selection and expansion for the active view: small
// models open expanded with no selection, large ones compact with the first
// card selected, and the weighted view carries no diagram state.
func (m *Model) initGraphState() {
	m.graphSel, m.graphExpanded, m.graphBounds, m.graphFocusEdges = "", nil, nil, false
	if m.graphView == 1 || len(m.graph.Types) == 0 {
		return
	}
	if graphIsSmall(m.graph) {
		m.graphExpanded = fga.ExpandAll(m.graph)
		return
	}
	m.graphExpanded = map[string]bool{}
	m.graphFocusEdges = true
	m.renderGraph()
	if len(m.graphBounds) > 0 {
		m.graphSel = m.graphBounds[0].Type
	}
}

// toggleGraphFocusEdges switches between drawing every edge and only the
// selected card's, keeping the view where it is.
func (m *Model) toggleGraphFocusEdges() {
	m.graphFocusEdges = !m.graphFocusEdges
	m.graphVP.SetContent(m.renderGraph())
	if m.graphFocusEdges {
		m.status = "showing " + m.graphSel + "'s lines — a for all"
	} else {
		m.status = "showing all lines — a for " + m.graphSel + "'s only"
	}
}

// cardBounds finds the last-rendered bounds of typ.
func (m *Model) cardBounds(typ string) (fga.CardBounds, bool) {
	for _, b := range m.graphBounds {
		if b.Type == typ {
			return b, true
		}
	}
	return fga.CardBounds{}, false
}

// selectGraphCard moves the selection by delta in render order (wrapping),
// re-renders, and pans horizontally so the card is visible. The caller scrolls
// vertically to the returned bounds.
func (m *Model) selectGraphCard(delta int) fga.CardBounds {
	n := len(m.graphBounds)
	if n == 0 {
		return fga.CardBounds{}
	}
	idx, found := 0, false
	for i, b := range m.graphBounds {
		if b.Type == m.graphSel {
			idx, found = i, true
			break
		}
	}
	switch {
	case found:
		idx = ((idx+delta)%n + n) % n
	case delta > 0:
		idx = 0
	default:
		idx = n - 1
	}
	m.graphSel = m.graphBounds[idx].Type
	m.graphVP.SetContent(m.renderGraph())
	b, _ := m.cardBounds(m.graphSel)
	m.revealGraphColumns(b)
	return b
}

// revealGraphColumns pans the viewport horizontally just enough to show b,
// keeping its left edge (and so its title) in view when b is wider than it.
func (m *Model) revealGraphColumns(b fga.CardBounds) {
	if x := m.graphVP.XOffset(); b.X < x {
		m.graphVP.SetXOffset(max(0, b.X-2))
	} else if w := m.graphVP.Width(); b.X+b.W > x+w {
		m.graphVP.SetXOffset(max(0, min(b.X+b.W-w+2, b.X-2)))
	}
}

// revealSelectedGraphCard moves the viewport, without animation, to the
// selected card when its title is off screen. A large model opens on its
// first card, which can sit far below or right of the canvas origin.
func (m *Model) revealSelectedGraphCard() {
	b, ok := m.cardBounds(m.graphSel)
	if !ok {
		return
	}
	m.revealGraphColumns(b)
	if title := b.Y + 1; title < m.graphVP.YOffset() || title >= m.graphVP.YOffset()+m.graphVP.Height() {
		m.snapGraphTo(b.Y)
	}
}

// hasRelations reports whether typ has relations to expand; leaf types
// render the same compact and expanded.
func (m *Model) hasRelations(typ string) bool {
	for _, t := range m.graph.Types {
		if t.Name == typ {
			return len(t.Relations) > 0
		}
	}
	return false
}

// toggleGraphCard expands or collapses the selected card, keeping its title on
// the same screen row.
func (m *Model) toggleGraphCard() {
	if !m.hasRelations(m.graphSel) {
		return
	}
	old, _ := m.cardBounds(m.graphSel)
	// The screen row counts the pinned legend, which can change height when
	// the expand hint appears or disappears.
	row := m.graphHeaderHeight() + old.Y - m.graphVP.YOffset()
	if m.graphExpanded == nil {
		m.graphExpanded = map[string]bool{}
	}
	m.graphExpanded[m.graphSel] = !m.graphExpanded[m.graphSel]
	m.graphVP.SetContent(m.renderGraph())
	if b, ok := m.cardBounds(m.graphSel); ok {
		m.snapGraphTo(max(0, m.graphHeaderHeight()+b.Y-row))
	}
	if m.graphExpanded[m.graphSel] {
		m.status = "expanded " + m.graphSel
	} else {
		m.status = "collapsed " + m.graphSel
	}
}

// toggleGraphAll collapses every card when all are expanded, else expands all.
func (m *Model) toggleGraphAll() {
	all := true
	for _, t := range m.graph.Types {
		if len(t.Relations) > 0 && !m.graphExpanded[t.Name] {
			all = false
			break
		}
	}
	if all {
		m.graphExpanded = map[string]bool{}
		m.status = "collapsed all types"
	} else {
		m.graphExpanded = fga.ExpandAll(m.graph)
		m.status = "expanded all types"
	}
	m.graphVP.SetContent(m.renderGraph())
	if b, ok := m.cardBounds(m.graphSel); ok {
		// Expanding or collapsing every card moves the columns, so the
		// selected card can land outside the visible columns too.
		m.revealGraphColumns(b)
		m.snapGraphTo(b.Y)
	}
}

// snapGraphTo jumps the vertical scroll to line y with no animation.
func (m *Model) snapGraphTo(y int) {
	y = min(max(0, y), m.graphMaxOffset())
	m.graphPos, m.graphVel, m.graphTarget = float64(y), 0, float64(y)
	m.graphAnimating = false
	m.graphVP.SetYOffset(y)
}

// resetGraphScroll snaps the graph viewport back to the top-left with no motion.
func (m *Model) resetGraphScroll() {
	m.graphPos = 0
	m.graphVel = 0
	m.graphTarget = 0
	m.graphAnimating = false
	m.graphVP.SetYOffset(0)
	m.graphVP.SetXOffset(0)
}

// graphMaxOffset is the furthest the graph viewport can scroll down.
func (m *Model) graphMaxOffset() int {
	maxOff := m.graphVP.TotalLineCount() - m.graphVP.Height()
	if maxOff < 0 {
		maxOff = 0
	}
	return maxOff
}

// panGraph shifts the graph viewport horizontally by cols (immediate).
func (m Model) panGraph(cols int) (tea.Model, tea.Cmd) {
	if cols >= 0 {
		m.graphVP.ScrollRight(cols)
	} else {
		m.graphVP.ScrollLeft(-cols)
	}
	return m, nil
}

// scrollGraph nudges the scroll target by delta lines (relative).
func (m Model) scrollGraph(delta int) (tea.Model, tea.Cmd) {
	return m.scrollGraphTo(m.graphTarget + float64(delta))
}

// scrollGraphTo sets an absolute scroll target (clamped) and ensures the
// animation loop is running. Repeated calls just retarget the in-flight spring.
func (m Model) scrollGraphTo(target float64) (tea.Model, tea.Cmd) {
	maxOff := float64(m.graphMaxOffset())
	if target < 0 {
		target = 0
	}
	if target > maxOff {
		target = maxOff
	}
	m.graphTarget = target
	if m.graphAnimating {
		// The running tick loop will ease toward the new target.
		return m, nil
	}
	m.graphAnimating = true
	return m, graphTick()
}

// advanceGraphScroll steps the spring one frame toward the target and schedules
// the next frame until the motion settles.
func (m Model) advanceGraphScroll() (tea.Model, tea.Cmd) {
	if !m.graphAnimating {
		return m, nil
	}
	m.graphPos, m.graphVel = m.graphSpring.Update(m.graphPos, m.graphVel, m.graphTarget)

	// Settle once we're within half a line of the target and nearly stopped,
	// so we don't tick forever on sub-pixel residue.
	if math.Abs(m.graphTarget-m.graphPos) < 0.5 && math.Abs(m.graphVel) < 0.5 {
		m.graphPos = m.graphTarget
		m.graphVel = 0
		m.graphAnimating = false
	}

	m.graphVP.SetYOffset(int(math.Round(m.graphPos)))

	if m.graphAnimating {
		return m, graphTick()
	}
	return m, nil
}
