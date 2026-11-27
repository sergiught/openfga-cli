package status

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
)

const (
	pending = iota
	running
	done
	skipped // derived by view: pending, though a later phase has started
)

// minCols is the narrowest terminal a card is drawn in.
const minCols = 20

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// phase is one step of the card.
type phase struct {
	name        string
	state       int
	note        string
	took        time.Duration
	step        string
	done, total int
	rate        float64
	left        time.Duration
}

// source is one party requests are sent to.
type source struct {
	name              string
	requests, limited int
	rate              float64
	pace              time.Duration
}

// state is everything the card shows.
type state struct {
	title    string
	elapsed  time.Duration
	frame    int
	phases   []phase
	sources  []source
	mem, top uint64
	data     []string // already styled
}

// palette holds the styles of one render. They are built per render because
// the style package's colors change with the theme.
type palette struct {
	bold, subtle, faint, rule, ok, warn, accent lipgloss.Style
	mono                                        bool
}

func newPalette() palette {
	return palette{
		bold:   lipgloss.NewStyle().Bold(true).Foreground(style.Fg),
		subtle: lipgloss.NewStyle().Foreground(style.Muted),
		faint:  lipgloss.NewStyle().Foreground(style.Faintc),
		rule:   lipgloss.NewStyle().Foreground(style.Subtle),
		ok:     lipgloss.NewStyle().Foreground(style.Green),
		warn:   lipgloss.NewStyle().Bold(true).Foreground(style.Amber),
		accent: lipgloss.NewStyle().Foreground(style.Primary),
		mono:   style.Active.Name == "mono",
	}
}

// bar is w cells: the gradient spans the whole track, so the fill reveals it.
func (p palette) bar(w int, frac float64) string {
	filled := max(0, min(w, int(frac*float64(w)+0.5)))
	if p.mono {
		return strings.Repeat("━", filled) + strings.Repeat("─", max(0, w-filled))
	}
	var b strings.Builder
	for i := range filled {
		k := 0.0
		if w > 1 {
			k = float64(i) / float64(w-1)
		}
		c := style.Blend(style.Primary, style.Secondary, k)
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("━"))
	}
	return b.String() + p.faint.Render(strings.Repeat("━", max(0, w-filled)))
}

// spread puts left and right at the two ends of w cells; left gives way.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(0, w-lipgloss.Width(right)-1), "…")
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

func (p palette) phaseLines(ph phase, frame, w, nameW int) []string {
	name := lipgloss.NewStyle().Width(nameW).Render(ph.name)
	switch ph.state {
	case done:
		return []string{spread(p.ok.Render(style.IconCheck)+" "+p.subtle.Render(name)+"  "+p.subtle.Render(ph.note), p.faint.Render(dur(ph.took)), w)}
	case pending:
		return []string{p.faint.Render(style.IconCircle + " " + name)}
	case skipped:
		return []string{p.faint.Render("–" + " " + name)}
	}
	head := p.accent.Render(spinner[frame%len(spinner)]) + " " + p.bold.Render(name) + "  "
	indent := strings.Repeat(" ", 2+nameW+2)
	if ph.total == 0 {
		right := ""
		if ph.done > 0 {
			right = p.bold.Render(num(ph.done))
			if ph.rate >= 1 {
				right += p.faint.Render(" · " + num(int(ph.rate)) + "/s")
			}
		}
		return []string{spread(head+ph.step, right, w)}
	}
	frac := min(1, float64(ph.done)/float64(ph.total))
	tail := p.bold.Render(fmt.Sprintf("%3d%%", int(frac*100)))
	if ph.left > 0 {
		tail += p.faint.Render(" · " + dur(ph.left) + " left")
	}
	barW := max(8, w-lipgloss.Width(indent)-lipgloss.Width(tail)-2)
	return []string{
		spread(head+ph.step, p.bold.Render(num(ph.done))+p.faint.Render(" / "+num(ph.total)), w),
		indent + p.bar(barW, frac) + "  " + tail,
	}
}

func (p palette) tile(label string, rows ...string) string {
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{p.faint.Render(label)}, rows...)...)
}

func (p palette) tiles(s state, w int) []string {
	nameW, numW := 0, 0
	for _, src := range s.sources {
		nameW = max(nameW, lipgloss.Width(src.name))
		numW = max(numW, len(num(src.requests)))
	}
	var req, lim []string
	for _, src := range s.sources {
		n := lipgloss.NewStyle().Width(nameW).Render(src.name)
		row := p.subtle.Render(n) + "  " + p.bold.Render(fmt.Sprintf("%*s", numW, num(src.requests)))
		if src.rate >= 0.05 {
			row += p.faint.Render(fmt.Sprintf("  %.1f/s", src.rate))
		}
		req = append(req, row)
		c := p.bold.Render(num(src.limited) + "×")
		if src.limited > 0 {
			c = p.warn.Render(num(src.limited) + "×")
		}
		row = p.subtle.Render(n) + "  " + c
		if src.pace > 0 {
			row += p.faint.Render("  1 per " + src.pace.Round(10*time.Millisecond).String())
		}
		lim = append(lim, row)
	}
	var parts []string
	if len(s.sources) > 0 {
		parts = append(parts, p.tile("REQUESTS", req...), p.tile("RATE LIMITED", lim...))
	}
	parts = append(parts, p.tile("MEMORY", p.bold.Render(size(s.mem)), p.faint.Render("peak "+size(s.top))))
	if len(parts) == 1 {
		return strings.Split(parts[0], "\n")
	}
	total := 0
	for _, part := range parts {
		total += lipgloss.Width(part)
	}
	if gap := (w - total) / (len(parts) - 1); gap >= 3 {
		pad := strings.Repeat(" ", min(gap, 6))
		joined := []string{parts[0]}
		for _, part := range parts[1:] {
			joined = append(joined, pad, part)
		}
		return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, joined...), "\n")
	}
	var out []string
	for i, part := range parts {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, strings.Split(part, "\n")...)
	}
	return out
}

func (p palette) dataLines(items []string, w int) []string {
	if len(items) == 0 {
		return nil
	}
	label := "DATA  "
	var out []string
	line := p.faint.Render(label)
	for i, it := range items {
		piece := it
		if i < len(items)-1 {
			piece += p.faint.Render(" ·")
		}
		if lipgloss.Width(line)+1+lipgloss.Width(piece) > w && lipgloss.Width(line) > len(label) {
			out = append(out, line)
			line = strings.Repeat(" ", len(label))
		}
		if lipgloss.Width(line) > len(label) {
			line += " "
		}
		line += piece
	}
	return append(out, line)
}

// view renders the card for a terminal of cols by rows. It has no side
// effects: the same inputs give the same output. When the card would not fit
// in rows-1 lines only the running phase is drawn. Every line is at most
// cols-1 cells wide.
func view(s state, cols, rows int) string {
	if cols < minCols || rows < 3 {
		return ""
	}
	s.phases = markSkipped(s.phases)
	p := newPalette()
	cardW := max(0, min(cols-1, 78))
	w := max(0, cardW-6) // border and two cells of padding each side
	nameW := 0
	for _, ph := range s.phases {
		nameW = max(nameW, lipgloss.Width(ph.name))
	}
	var body []string
	body = append(body, "")
	for _, ph := range s.phases {
		body = append(body, p.phaseLines(ph, s.frame, w, nameW)...)
	}
	body = append(body, "", p.rule.Render(strings.Repeat("─", w)), "")
	body = append(body, p.tiles(s, w)...)
	if d := p.dataLines(s.data, w); d != nil {
		body = append(body, "")
		body = append(body, d...)
	}
	body = append(body, "")

	if len(body)+2 > rows-1 {
		var lines []string
		for _, ph := range s.phases {
			if ph.state == running {
				lines = append(lines, p.phaseLines(ph, s.frame, cols-1, nameW)...)
			}
		}
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, cols-1, "…")
		}
		return strings.Join(lines, "\n")
	}

	title := " " + s.title + " "
	clock := " " + dur(s.elapsed) + " "
	fill := max(0, cardW-2-1-lipgloss.Width(title)-lipgloss.Width(clock)-1)
	top := p.rule.Render("╭─") + style.Gradient(title) + p.rule.Render(strings.Repeat("─", fill)) + p.subtle.Render(clock) + p.rule.Render("─╮")
	var b strings.Builder
	b.WriteString(ansi.Truncate(top, cols-1, "") + "\n")
	for _, l := range body {
		l = ansi.Truncate(l, w, "…")
		b.WriteString(ansi.Truncate(p.rule.Render("│")+"  "+l+strings.Repeat(" ", max(0, w-lipgloss.Width(l)))+"  "+p.rule.Render("│"), cols-1, "") + "\n")
	}
	b.WriteString(ansi.Truncate(p.rule.Render("╰"+strings.Repeat("─", max(0, cardW-2))+"╯"), cols-1, ""))
	return b.String()
}

// markSkipped returns phases with each pending one that has a started phase
// after it marked skipped: it was never needed, and is not still to come.
func markSkipped(phases []phase) []phase {
	out := slices.Clone(phases)
	started := false
	for i := len(out) - 1; i >= 0; i-- {
		switch {
		case out[i].state != pending:
			started = true
		case started:
			out[i] = phase{name: out[i].name, state: skipped}
		}
	}
	return out
}
