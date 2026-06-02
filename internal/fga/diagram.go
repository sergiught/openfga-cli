package fga

import (
	"container/heap"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sergiught/openfga-cli/internal/style"
)

// RenderDiagram draws the authorization model as a node-link diagram: one
// rounded card per object type (a colored header plus its relations) laid out
// left→right by dependency depth, with edges routed orthogonally around the
// cards (never through them) and colored by resolution kind. The result is a
// wide, multi-line string meant to be shown inside a scrollable/pannable
// viewport.
func (g Graph) RenderDiagram() string {
	if len(g.Types) == 0 {
		return style.Faint.Render("no authorization model in this store")
	}

	boxes := g.buildBoxes()
	layoutBoxes(boxes, g.Edges)

	w, h := canvasBounds(boxes)
	c := newCanvas(w, h)

	routeEdges(c, boxes, g.Edges)
	for _, b := range boxes {
		drawBox(c, b)
	}
	drawPorts(c, boxes, g.Edges)

	return g.diagramHeader() + "\n\n" + c.String()
}

func (g Graph) diagramHeader() string {
	legend := strings.Join([]string{
		colorRune('─', style.Green) + " " + style.Faint.Render("direct"),
		colorRune('─', style.Amber) + " " + style.Faint.Render("inherited (tuple-to-userset)"),
		style.Faint.Render("↑↓←→ pan"),
	}, "    ")
	return style.Subtitle.Render("schema "+g.SchemaVersion) + "    " + legend
}

// --- node boxes ---

type scell struct {
	r    rune
	fg   lipgloss.TerminalColor
	bg   lipgloss.TerminalColor
	bold bool
}

type nodeBox struct {
	typ    string
	title  []scell
	rows   [][]scell
	innerW int
	w, h   int
	x, y   int
	col    int
	order  int // position within its column
}

const (
	hgap        = 10 // horizontal space between columns
	vgap        = 3  // vertical space between stacked boxes (edge lanes)
	margin      = 3  // free border around the whole canvas for detours
	maxRowWidth = 30
)

func (g Graph) buildBoxes() []*nodeBox {
	boxes := make([]*nodeBox, 0, len(g.Types))
	for _, t := range g.Types {
		b := &nodeBox{typ: t.Name}
		b.title = styledRunes(t.Name, style.OnAccent)
		for i := range b.title {
			b.title[i].bold = true
		}
		for _, r := range t.Relations {
			b.rows = append(b.rows, relationRow(r))
		}
		b.innerW = len(b.title)
		for _, row := range b.rows {
			if len(row) > b.innerW {
				b.innerW = len(row)
			}
		}
		if b.innerW > maxRowWidth {
			b.innerW = maxRowWidth
		}
		b.w = b.innerW + 4 // borders + one space of padding each side
		b.h = 3            // top border + title + bottom border
		if len(b.rows) > 0 {
			b.h += 1 + len(b.rows) // separator + one line per relation
		}
		boxes = append(boxes, b)
	}
	return boxes
}

// relationRow renders one relation line: the relation name followed by compact,
// colored markers for each of its resolution edges.
func relationRow(r Relation) []scell {
	cells := styledRunes(r.Name, style.Cyan)
	for _, e := range r.Edges {
		cells = append(cells, scell{r: ' '})
		switch e.Kind {
		case "direct":
			cells = append(cells, scell{r: '←', fg: style.Green})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		case "computed":
			cells = append(cells, scell{r: '=', fg: style.Cyan})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		case "ttu":
			cells = append(cells, scell{r: '⇡', fg: style.Amber})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		}
	}
	if len(cells) > maxRowWidth {
		cells = cells[:maxRowWidth-1]
		cells = append(cells, scell{r: '…', fg: style.Faintc})
	}
	return cells
}

func styledRunes(s string, fg lipgloss.TerminalColor) []scell {
	rs := []rune(s)
	out := make([]scell, len(rs))
	for i, r := range rs {
		out[i] = scell{r: r, fg: fg}
	}
	return out
}

// --- layout ---

func layoutBoxes(boxes []*nodeBox, edges []DiagramEdge) {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}

	// Dependency depth (longest path along From→To), cycle-guarded.
	memo := map[string]int{}
	var depthOf func(t string, stack map[string]bool) int
	depthOf = func(t string, stack map[string]bool) int {
		if d, ok := memo[t]; ok {
			return d
		}
		if stack[t] {
			return 0
		}
		stack[t] = true
		best := 0
		for _, to := range adj[t] {
			if d := depthOf(to, stack) + 1; d > best {
				best = d
			}
		}
		stack[t] = false
		memo[t] = best
		return best
	}

	maxDepth := 0
	for _, b := range boxes {
		if d := depthOf(b.typ, map[string]bool{}); d > maxDepth {
			maxDepth = d
		}
	}
	for _, b := range boxes {
		b.col = maxDepth - depthOf(b.typ, map[string]bool{})
	}

	cols := map[int][]*nodeBox{}
	maxCol := 0
	for _, b := range boxes {
		b.order = len(cols[b.col])
		cols[b.col] = append(cols[b.col], b)
		if b.col > maxCol {
			maxCol = b.col
		}
	}

	orderColumns(cols, maxCol, edges)

	// Column x positions.
	colX := make([]int, maxCol+1)
	x := margin
	for c := 0; c <= maxCol; c++ {
		colX[c] = x
		cw := 0
		for _, b := range cols[c] {
			if b.w > cw {
				cw = b.w
			}
		}
		x += cw + hgap
	}

	// Vertically center each column.
	canvasH := 0
	for c := 0; c <= maxCol; c++ {
		if ch := columnHeight(cols[c]); ch > canvasH {
			canvasH = ch
		}
	}
	for c := 0; c <= maxCol; c++ {
		y := margin + (canvasH-columnHeight(cols[c]))/2
		for _, b := range cols[c] {
			b.x = colX[c]
			b.y = y
			y += b.h + vgap
		}
	}
}

// orderColumns reduces edge crossings with a few barycenter sweeps, ordering
// each column by the average position of its neighbors in the prior column.
func orderColumns(cols map[int][]*nodeBox, maxCol int, edges []DiagramEdge) {
	pos := map[string]int{}
	refresh := func() {
		for c := 0; c <= maxCol; c++ {
			for i, b := range cols[c] {
				b.order = i
				pos[b.typ] = i
			}
		}
	}
	refresh()

	preds := map[string][]string{} // box -> neighbors in the column to its left
	succs := map[string][]string{}
	colOf := map[string]int{}
	for c := 0; c <= maxCol; c++ {
		for _, b := range cols[c] {
			colOf[b.typ] = c
		}
	}
	for _, e := range edges {
		// Edges run From (smaller col) → To (larger col) in the common case.
		if colOf[e.From] < colOf[e.To] {
			succs[e.From] = append(succs[e.From], e.To)
			preds[e.To] = append(preds[e.To], e.From)
		} else if colOf[e.To] < colOf[e.From] {
			succs[e.To] = append(succs[e.To], e.From)
			preds[e.From] = append(preds[e.From], e.To)
		}
	}

	bary := func(neighbors []string) float64 {
		if len(neighbors) == 0 {
			return -1
		}
		sum := 0
		for _, n := range neighbors {
			sum += pos[n]
		}
		return float64(sum) / float64(len(neighbors))
	}

	for sweep := 0; sweep < 4; sweep++ {
		forward := sweep%2 == 0
		for step := 0; step <= maxCol; step++ {
			c := step
			if !forward {
				c = maxCol - step
			}
			nbr := preds
			if !forward {
				nbr = succs
			}
			list := cols[c]
			sort.SliceStable(list, func(i, j int) bool {
				bi, bj := bary(nbr[list[i].typ]), bary(nbr[list[j].typ])
				if bi < 0 || bj < 0 {
					return false // keep relative order for unconstrained nodes
				}
				return bi < bj
			})
			refresh()
		}
	}
}

func columnHeight(boxes []*nodeBox) int {
	h := 0
	for i, b := range boxes {
		h += b.h
		if i < len(boxes)-1 {
			h += vgap
		}
	}
	return h
}

func canvasBounds(boxes []*nodeBox) (w, h int) {
	for _, b := range boxes {
		if r := b.x + b.w; r > w {
			w = r
		}
		if bottom := b.y + b.h; bottom > h {
			h = bottom
		}
	}
	return w + margin, h + margin
}

// --- canvas ---

type canvas struct {
	w, h  int
	cells [][]scell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]scell, h)}
	for y := range c.cells {
		row := make([]scell, w)
		for x := range row {
			row[x] = scell{r: ' '}
		}
		c.cells[y] = row
	}
	return c
}

func (c *canvas) set(x, y int, s scell) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y][x] = s
}

func (c *canvas) at(x, y int) rune {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return ' '
	}
	return c.cells[y][x].r
}

func (c *canvas) String() string {
	var b strings.Builder
	for y := 0; y < c.h; y++ {
		row := c.cells[y]
		end := c.w
		for end > 0 && row[end-1].r == ' ' && row[end-1].bg == nil {
			end--
		}
		x := 0
		for x < end {
			s := row[x]
			j := x
			var seg []rune
			for j < end && row[j].fg == s.fg && row[j].bg == s.bg && row[j].bold == s.bold {
				seg = append(seg, row[j].r)
				j++
			}
			st := lipgloss.NewStyle()
			if s.fg != nil {
				st = st.Foreground(s.fg)
			}
			if s.bg != nil {
				st = st.Background(s.bg)
			}
			if s.bold {
				st = st.Bold(true)
			}
			if s.fg == nil && s.bg == nil && !s.bold {
				b.WriteString(string(seg))
			} else {
				b.WriteString(st.Render(string(seg)))
			}
			x = j
		}
		if y < c.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// --- box drawing ---

func drawBox(c *canvas, b *nodeBox) {
	bc := style.Subtle
	left, right := b.x, b.x+b.w-1
	top := b.y
	innerStart := b.x + 2

	c.set(left, top, scell{r: '╭', fg: bc})
	c.set(right, top, scell{r: '╮', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, top, scell{r: '─', fg: bc})
	}

	// Title row with a filled accent header bar.
	ty := top + 1
	c.set(left, ty, scell{r: '│', fg: bc})
	c.set(right, ty, scell{r: '│', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, ty, scell{r: ' ', bg: style.Primary})
	}
	for i := 0; i < b.innerW; i++ {
		s := scell{r: ' ', fg: style.OnAccent, bg: style.Primary, bold: true}
		if i < len(b.title) {
			s.r = b.title[i].r
		}
		c.set(innerStart+i, ty, s)
	}

	rowY := ty + 1
	if len(b.rows) > 0 {
		c.set(left, rowY, scell{r: '├', fg: bc})
		c.set(right, rowY, scell{r: '┤', fg: bc})
		for x := left + 1; x < right; x++ {
			c.set(x, rowY, scell{r: '─', fg: bc})
		}
		rowY++
		for _, row := range b.rows {
			c.set(left, rowY, scell{r: '│', fg: bc})
			c.set(right, rowY, scell{r: '│', fg: bc})
			for i := 0; i < b.innerW; i++ {
				s := scell{r: ' '}
				if i < len(row) {
					s = row[i]
				}
				c.set(innerStart+i, rowY, s)
			}
			rowY++
		}
	}

	bottom := b.y + b.h - 1
	c.set(left, bottom, scell{r: '╰', fg: bc})
	c.set(right, bottom, scell{r: '╯', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, bottom, scell{r: '─', fg: bc})
	}
}

// drawPorts marks the box borders where edges connect, so connection points are
// obvious after the boxes are painted over the routed lines.
func drawPorts(c *canvas, boxes []*nodeBox, edges []DiagramEdge) {
	byType := indexBoxes(boxes)
	for _, e := range edges {
		src, dst := byType[e.From], byType[e.To]
		if src == nil || dst == nil {
			continue
		}
		col := edgeColor(e.Kind)
		sy := portRow(src, e, edges, true)
		ty := portRow(dst, e, edges, false)
		c.set(src.x+src.w-1, sy, scell{r: '├', fg: col})
		c.set(dst.x, ty, scell{r: '┤', fg: col})
	}
}

// --- edge routing (A* around the boxes) ---

func routeEdges(c *canvas, boxes []*nodeBox, edges []DiagramEdge) {
	blocked := obstacleGrid(c, boxes)
	used := map[[2]int]bool{}
	byType := indexBoxes(boxes)

	// Stable order so port assignment is deterministic.
	sorted := append([]DiagramEdge(nil), edges...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].From != sorted[j].From {
			return sorted[i].From < sorted[j].From
		}
		return sorted[i].To < sorted[j].To
	})

	for _, e := range sorted {
		src, dst := byType[e.From], byType[e.To]
		if src == nil || dst == nil {
			continue
		}
		sy := portRow(src, e, edges, true)
		ty := portRow(dst, e, edges, false)
		start := [2]int{src.x + src.w, sy} // gutter cell right of source
		goal := [2]int{dst.x - 1, ty}       // gutter cell left of target
		path := routeAStar(c, blocked, used, start, goal)
		if path == nil {
			continue
		}
		paintPath(c, path, used, edgeColor(e.Kind))
	}
}

func obstacleGrid(c *canvas, boxes []*nodeBox) [][]bool {
	g := make([][]bool, c.h)
	for y := range g {
		g[y] = make([]bool, c.w)
	}
	for _, b := range boxes {
		for y := b.y; y < b.y+b.h; y++ {
			for x := b.x; x < b.x+b.w; x++ {
				if y >= 0 && y < c.h && x >= 0 && x < c.w {
					g[y][x] = true
				}
			}
		}
	}
	return g
}

// portRow distributes a box's edges across its vertical extent so multiple
// edges connect at distinct rows instead of stacking on one.
func portRow(b *nodeBox, e DiagramEdge, edges []DiagramEdge, outgoing bool) int {
	var group []DiagramEdge
	for _, x := range edges {
		if (outgoing && x.From == b.typ) || (!outgoing && x.To == b.typ) {
			group = append(group, x)
		}
	}
	sort.SliceStable(group, func(i, j int) bool {
		a, c := group[i], group[j]
		if outgoing {
			if a.To != c.To {
				return a.To < c.To
			}
			return a.Kind < c.Kind
		}
		if a.From != c.From {
			return a.From < c.From
		}
		return a.Kind < c.Kind
	})
	idx, total := 0, len(group)
	for i, x := range group {
		if x == e {
			idx = i
			break
		}
	}
	lo, hi := b.y+1, b.y+b.h-2
	if hi < lo {
		hi = lo
	}
	if total <= 1 {
		return (lo + hi) / 2
	}
	row := lo + (idx+1)*(hi-lo)/(total+1)
	if row < lo {
		row = lo
	}
	if row > hi {
		row = hi
	}
	return row
}

// dirs: 0=E 1=W 2=S 3=N
var dirDX = [4]int{1, -1, 0, 0}
var dirDY = [4]int{0, 0, 1, -1}

func opposite(d int) int {
	switch d {
	case 0:
		return 1
	case 1:
		return 0
	case 2:
		return 3
	default:
		return 2
	}
}

type aStarNode struct {
	x, y, dir int
	g, f      int
	index     int
}

type pq []*aStarNode

func (p pq) Len() int            { return len(p) }
func (p pq) Less(i, j int) bool  { return p[i].f < p[j].f }
func (p pq) Swap(i, j int)       { p[i], p[j] = p[j], p[i]; p[i].index = i; p[j].index = j }
func (p *pq) Push(x any)         { n := x.(*aStarNode); n.index = len(*p); *p = append(*p, n) }
func (p *pq) Pop() any           { old := *p; n := old[len(old)-1]; *p = old[:len(old)-1]; return n }

const (
	stepCost    = 1
	turnPenalty = 5
	reusePenalty = 3
)

// routeAStar finds an orthogonal path from start to goal avoiding blocked cells,
// preferring straight runs (turn penalty) and avoiding already-drawn edge cells
// (reuse penalty) so parallel edges fan out into separate channels.
func routeAStar(c *canvas, blocked [][]bool, used map[[2]int]bool, start, goal [2]int) [][2]int {
	if oob(c, start[0], start[1]) || oob(c, goal[0], goal[1]) {
		return nil
	}
	h := func(x, y int) int { return abs(x-goal[0]) + abs(y-goal[1]) }
	type key struct{ x, y, dir int }
	best := map[key]int{}
	parent := map[key]key{}
	hasParent := map[key]bool{}

	open := &pq{}
	heap.Init(open)
	startDir := 0 // bias toward exiting east
	sk := key{start[0], start[1], startDir}
	best[sk] = 0
	heap.Push(open, &aStarNode{x: start[0], y: start[1], dir: startDir, g: 0, f: h(start[0], start[1])})

	var goalKey key
	found := false
	for open.Len() > 0 {
		cur := heap.Pop(open).(*aStarNode)
		ck := key{cur.x, cur.y, cur.dir}
		if g, ok := best[ck]; ok && cur.g > g {
			continue
		}
		// Require the target to be entered heading east so the arrowhead always
		// points into the card rather than running vertically alongside it.
		if cur.x == goal[0] && cur.y == goal[1] && cur.dir == 0 {
			goalKey = ck
			found = true
			break
		}
		for d := 0; d < 4; d++ {
			if d == opposite(cur.dir) {
				continue
			}
			nx, ny := cur.x+dirDX[d], cur.y+dirDY[d]
			if oob(c, nx, ny) || blocked[ny][nx] {
				continue
			}
			ng := cur.g + stepCost
			if d != cur.dir {
				ng += turnPenalty
			}
			if used[[2]int{nx, ny}] {
				ng += reusePenalty
			}
			nk := key{nx, ny, d}
			if g, ok := best[nk]; !ok || ng < g {
				best[nk] = ng
				parent[nk] = ck
				hasParent[nk] = true
				heap.Push(open, &aStarNode{x: nx, y: ny, dir: d, g: ng, f: ng + h(nx, ny)})
			}
		}
	}
	if !found {
		return nil
	}
	var path [][2]int
	k := goalKey
	for {
		path = append([][2]int{{k.x, k.y}}, path...)
		if !hasParent[k] {
			break
		}
		k = parent[k]
	}
	return path
}

// paintPath stamps a routed path with rounded corners, crossing glyphs, and a
// terminal arrowhead.
func paintPath(c *canvas, path [][2]int, used map[[2]int]bool, col lipgloss.TerminalColor) {
	dirOf := func(a, b [2]int) int {
		switch {
		case b[0] > a[0]:
			return 0
		case b[0] < a[0]:
			return 1
		case b[1] > a[1]:
			return 2
		default:
			return 3
		}
	}
	for i, p := range path {
		used[[2]int{p[0], p[1]}] = true
		var r rune
		switch {
		case i == len(path)-1:
			in := dirOf(path[i-1], p)
			r = arrowFor(in)
		case i == 0:
			out := dirOf(p, path[i+1])
			r = lineFor(out)
		default:
			in := dirOf(path[i-1], p)
			out := dirOf(p, path[i+1])
			r = glyphFor(in, out)
		}
		// Merge a straight crossing into a junction.
		if cur := c.at(p[0], p[1]); (cur == '│' && r == '─') || (cur == '─' && r == '│') {
			r = '┼'
		}
		c.set(p[0], p[1], scell{r: r, fg: col})
	}
}

func lineFor(dir int) rune {
	if dir == 0 || dir == 1 {
		return '─'
	}
	return '│'
}

func glyphFor(in, out int) rune {
	if in == out {
		return lineFor(out)
	}
	switch {
	case (in == 0 && out == 2) || (in == 3 && out == 1):
		return '╮'
	case (in == 0 && out == 3) || (in == 2 && out == 1):
		return '╯'
	case (in == 1 && out == 2) || (in == 3 && out == 0):
		return '╭'
	case (in == 1 && out == 3) || (in == 2 && out == 0):
		return '╰'
	}
	return lineFor(out)
}

func arrowFor(dir int) rune {
	switch dir {
	case 0:
		return '▸'
	case 1:
		return '◂'
	case 2:
		return '▾'
	default:
		return '▴'
	}
}

// --- helpers ---

func indexBoxes(boxes []*nodeBox) map[string]*nodeBox {
	m := map[string]*nodeBox{}
	for _, b := range boxes {
		m[b.typ] = b
	}
	return m
}

func edgeColor(kind string) lipgloss.TerminalColor {
	if kind == "ttu" {
		return style.Amber
	}
	return style.Green
}

func colorRune(r rune, fg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(fg).Render(string(r))
}

func oob(c *canvas, x, y int) bool { return x < 0 || y < 0 || x >= c.w || y >= c.h }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
