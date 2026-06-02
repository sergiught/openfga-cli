package playground

import (
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	maxOff := m.graphVP.TotalLineCount() - m.graphVP.Height
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
