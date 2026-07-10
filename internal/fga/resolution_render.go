package fga

import (
	"slices"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// This file draws a ResNode tree as a top-down node-link diagram on the shared
// canvas: each node is a rounded box, parents sit centered above their children,
// and box-drawing connectors join them. Nodes present in the `granted` set are
// drawn in the success accent — the branch(es) that reach the queried user.

const (
	resBoxH   = 3           // rows per box: top border, label, bottom border
	resGap    = 3           // horizontal gap between sibling subtrees
	resChildY = resBoxH + 1 // a child box's top relative to its parent's top (one bus row)
)

// resLabel is the one-line label shown inside a node's box.
func resLabel(n *ResNode) string {
	switch n.Op {
	case ResUnion, ResIntersection, ResExclusion:
		return n.Name
	}
	switch {
	case len(n.Users) > 0:
		return strings.Join(n.Users, ", ")
	case n.Computed != "":
		return n.Computed
	case n.TTUFrom != "":
		return strings.Join(n.TTUTo, ", ") + " from " + n.TTUFrom
	}
	return n.Name
}

type resBox struct {
	node  *ResNode
	label string
	boxW  int
	subW  int
	kids  []*resBox
	x, y  int // top-left of the box, filled during placement
	cx    int // box center column
}

func layoutResBox(n *ResNode) *resBox {
	b := &resBox{node: n, label: resLabel(n)}
	b.boxW = lipgloss.Width(b.label) + 4
	for _, c := range n.Children {
		b.kids = append(b.kids, layoutResBox(c))
	}
	if len(b.kids) == 0 {
		b.subW = b.boxW
		return b
	}
	total := 0
	for i, k := range b.kids {
		if i > 0 {
			total += resGap
		}
		total += k.subW
	}
	b.subW = max(total, b.boxW)
	return b
}

func placeResBox(b *resBox, leftX, topY int) {
	b.y = topY
	b.x = leftX + (b.subW-b.boxW)/2
	b.cx = b.x + b.boxW/2
	if len(b.kids) == 0 {
		return
	}
	childrenW := 0
	for i, k := range b.kids {
		if i > 0 {
			childrenW += resGap
		}
		childrenW += k.subW
	}
	cx := leftX + (b.subW-childrenW)/2
	for _, k := range b.kids {
		placeResBox(k, cx, topY+resChildY)
		cx += k.subW + resGap
	}
}

func resTreeHeight(b *resBox) int {
	if len(b.kids) == 0 {
		return resBoxH
	}
	ch := 0
	for _, k := range b.kids {
		ch = max(ch, resTreeHeight(k))
	}
	return resChildY + ch
}

// RenderResolution draws root as a top-down node-link diagram. Nodes in granted
// are drawn in the success accent (the branch that reaches the user); pass nil
// to draw the whole tree neutrally.
func RenderResolution(root *ResNode, granted map[*ResNode]bool) string {
	if root == nil {
		return ""
	}
	b := layoutResBox(root)
	placeResBox(b, 0, 0)
	c := newCanvas(b.subW+1, resTreeHeight(b))
	drawResTree(c, b, granted)
	return c.String()
}

func drawResTree(c *canvas, b *resBox, granted map[*ResNode]bool) {
	drawResBox(c, b, granted[b.node])
	for _, k := range b.kids {
		drawResTree(c, k, granted)
	}
	if len(b.kids) > 0 {
		drawResConnectors(c, b, granted)
	}
}

func drawResBox(c *canvas, b *resBox, on bool) {
	edge, text := style.Subtle, style.Muted
	if on {
		edge, text = style.Green, style.Green
	}
	l, r := b.x, b.x+b.boxW-1
	t, bot := b.y, b.y+2
	c.set(l, t, scell{r: '╭', fg: edge})
	c.set(r, t, scell{r: '╮', fg: edge})
	c.set(l, bot, scell{r: '╰', fg: edge})
	c.set(r, bot, scell{r: '╯', fg: edge})
	for x := l + 1; x < r; x++ {
		c.set(x, t, scell{r: '─', fg: edge})
		c.set(x, bot, scell{r: '─', fg: edge})
	}
	c.set(l, t+1, scell{r: '│', fg: edge})
	c.set(r, t+1, scell{r: '│', fg: edge})
	inner := b.boxW - 2
	runes := []rune(b.label)
	pad := (inner - len(runes)) / 2
	for i := 0; i < inner; i++ {
		c.set(l+1+i, t+1, scell{r: ' '})
	}
	for i, rr := range runes {
		c.set(l+1+pad+i, t+1, scell{r: rr, fg: text, bold: on})
	}
}

func drawResConnectors(c *canvas, b *resBox, granted map[*ResNode]bool) {
	edge := style.Subtle
	if granted[b.node] {
		edge = style.Green
	}
	px := b.cx
	busY := b.y + 3
	centers := make([]int, len(b.kids))
	minC, maxC := px, px
	for i, k := range b.kids {
		centers[i] = k.cx
		minC, maxC = min(minC, k.cx), max(maxC, k.cx)
	}
	// Parent stem down into the bus.
	c.set(px, b.y+2, scell{r: '┬', fg: edge})
	for x := minC; x <= maxC; x++ {
		up := x == px
		down := slices.Contains(centers, x)
		left := x > minC
		right := x < maxC
		c.set(x, busY, scell{r: busRune(up, down, left, right), fg: edge})
	}
	// Each child's top border receives the drop.
	for i, k := range b.kids {
		ke := edge
		if granted[k.node] {
			ke = style.Green
		}
		c.set(centers[i], k.y, scell{r: '┴', fg: ke})
	}
}

// busRune picks the box-drawing glyph for a connector cell from which sides it
// links to.
func busRune(up, down, left, right bool) rune {
	switch {
	case up && down && left && right:
		return '┼'
	case up && down && right:
		return '├'
	case up && down && left:
		return '┤'
	case down && left && right:
		return '┬'
	case up && left && right:
		return '┴'
	case up && down:
		return '│'
	case down && right:
		return '╭'
	case down && left:
		return '╮'
	case up && right:
		return '╰'
	case up && left:
		return '╯'
	case left || right:
		return '─'
	default:
		return '│'
	}
}
