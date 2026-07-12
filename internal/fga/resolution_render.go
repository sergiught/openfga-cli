package fga

import (
	"image/color"
	"slices"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// This file renders a Check resolution as a top-down node-link diagram styled
// after the OpenFGA playground: the queried object on top, the userset
// relations it resolves through in the middle, a "Direct Users" grouping for
// directly-assigned users, and the granted users at the bottom. Endpoints (the
// object and the users) are the tinted nodes; the userset / grouping nodes stay
// muted; connectors and endpoints on the branch that reaches the queried user
// are drawn in the success accent.

// --- display tree ---
//
// The logical ResNode tree mirrors the raw Expand response, where a relation is
// a bare union/leaf and computed / tuple-to-userset references are dead-end
// leaves. For display we transform it into a richer tree that names the object,
// surfaces every relation as its own box, and groups direct users under an
// explicit "Direct Users" node.

type dispKind int

const (
	dispObject   dispKind = iota // the queried object — an endpoint
	dispRelation                 // an object#relation userset — an intermediate
	dispGroup                    // a "Direct Users" grouping — an intermediate
	dispUser                     // a concrete user / userset — an endpoint
)

type dispNode struct {
	label   string
	edge    string // label on the edge from the parent to this node ("owner from")
	kind    dispKind
	granted bool
	kids    []*dispNode
}

// buildDisplay wraps a resolution tree (rooted at object#relation) in the object
// node and labels the object→relation edge with "<relation> from", matching the
// playground layout.
func buildDisplay(root *ResNode, user, object, relation string) *dispNode {
	rel := asRelation(dispFromRes(root, user), object+"#"+relation, root.Granted)
	rel.edge = relation + " from"
	return &dispNode{
		label:   object,
		kind:    dispObject,
		granted: root.Granted,
		kids:    []*dispNode{rel},
	}
}

// dispFromRes maps one resolution node to a display node. Combinators become
// relation boxes over their operands; direct-user leaves become a "Direct Users"
// group over the user boxes; expanded computed / tuple-to-userset leaves become
// the relation they point at, carrying their expansion below.
func dispFromRes(n *ResNode, user string) *dispNode {
	switch n.Op {
	case ResUnion, ResIntersection, ResExclusion:
		d := &dispNode{label: n.Name, kind: dispRelation, granted: n.Granted}
		for _, c := range n.Children {
			d.kids = append(d.kids, dispFromRes(c, user))
		}
		return d
	}
	switch {
	case n.Computed != "":
		if len(n.Children) > 0 {
			// Expanded: fold into the relation it points at so the reference box
			// and its expansion don't render as two identical stacked boxes.
			return asRelation(dispFromRes(n.Children[0], user), n.Computed, n.Granted)
		}
		return &dispNode{label: n.Computed, kind: dispRelation, granted: n.Granted}
	case n.TTUFrom != "":
		d := &dispNode{label: ttuLabel(n), kind: dispRelation, granted: n.Granted}
		for _, c := range n.Children {
			d.kids = append(d.kids, asRelation(dispFromRes(c, user), c.Name, c.Granted))
		}
		return d
	default:
		// A direct-users leaf (possibly empty).
		g := &dispNode{label: "Direct Users", kind: dispGroup, granted: n.Granted}
		for _, u := range n.Users {
			g.kids = append(g.kids, &dispNode{label: u, kind: dispUser, granted: u == user})
		}
		return g
	}
}

// asRelation guarantees inner is presented as a relation box named `name`,
// wrapping it when it isn't already one (e.g. a bare "Direct Users" group), so a
// relation always shows its own labeled box above whatever it resolves to.
func asRelation(inner *dispNode, name string, granted bool) *dispNode {
	if inner.kind == dispRelation {
		return inner
	}
	return &dispNode{label: name, kind: dispRelation, granted: granted, kids: []*dispNode{inner}}
}

func ttuLabel(n *ResNode) string {
	return strings.Join(n.TTUTo, ", ") + " from " + n.TTUFrom
}

// --- layout ---

const (
	dbBoxH      = 3 // rows per box: top border, label, bottom border
	dbSiblings  = 3 // horizontal gap between sibling subtrees
	dbBusEdge   = 1 // connector rows below a node that fans out to several children
	dbLineEdge  = 2 // connector rows on a plain single-child edge
	dbLabelEdge = 4 // connector rows on a labeled single-child edge (arrow, label, line)
)

type dbox struct {
	node *dispNode
	boxW int
	subW int
	kids []*dbox
	x, y int
	cx   int
}

// childGap is the number of connector rows between a box and its children: a
// single bus row when it branches, a taller lane for a single child so a plain
// edge breathes and a labeled edge has room for its text.
func childGap(b *dbox) int {
	if len(b.kids) == 1 {
		if b.kids[0].node.edge != "" {
			return dbLabelEdge
		}
		return dbLineEdge
	}
	return dbBusEdge
}

func layoutDBox(n *dispNode) *dbox {
	b := &dbox{node: n, boxW: lipgloss.Width(n.label) + 4}
	for _, k := range n.kids {
		b.kids = append(b.kids, layoutDBox(k))
	}
	if len(b.kids) == 0 {
		b.subW = b.boxW
		return b
	}
	total := 0
	for i, k := range b.kids {
		if i > 0 {
			total += dbSiblings
		}
		total += k.subW
	}
	b.subW = max(total, b.boxW)
	return b
}

func placeDBox(b *dbox, leftX, topY int) {
	b.y = topY
	b.x = leftX + (b.subW-b.boxW)/2
	b.cx = b.x + b.boxW/2
	if len(b.kids) == 0 {
		return
	}
	childTop := topY + dbBoxH + childGap(b)
	childrenW := 0
	for i, k := range b.kids {
		if i > 0 {
			childrenW += dbSiblings
		}
		childrenW += k.subW
	}
	cx := leftX + (b.subW-childrenW)/2
	for _, k := range b.kids {
		placeDBox(k, cx, childTop)
		cx += k.subW + dbSiblings
	}
}

func dboxHeight(b *dbox) int {
	if len(b.kids) == 0 {
		return dbBoxH
	}
	ch := 0
	for _, k := range b.kids {
		ch = max(ch, dboxHeight(k))
	}
	return dbBoxH + childGap(b) + ch
}

// RenderResolution draws the resolution rooted at object#relation as the
// playground-style node-link diagram. user is the queried user (its box is
// highlighted); nodes and connectors on the granting branch are tinted.
func RenderResolution(root *ResNode, user, object, relation string) string {
	if root == nil {
		return ""
	}
	d := buildDisplay(root, user, object, relation)
	b := layoutDBox(d)
	placeDBox(b, 0, 0)
	c := newCanvas(b.subW+1, dboxHeight(b))
	drawDTree(c, b)
	return c.String()
}

// --- drawing ---

func drawDTree(c *canvas, b *dbox) {
	drawDBox(c, b)
	for _, k := range b.kids {
		drawDTree(c, k)
	}
	switch {
	case len(b.kids) == 1:
		drawVEdge(c, b, b.kids[0])
	case len(b.kids) > 1:
		drawBus(c, b)
	}
}

// dboxColors picks a node's border/text tint: endpoints (object, user) light up
// green when granted and stay subtle otherwise; the userset / grouping
// intermediates always render muted, letting the connectors carry the path.
func dboxColors(n *dispNode) (edge, text color.Color, bold bool) {
	if n.kind == dispObject || n.kind == dispUser {
		if n.granted {
			return style.Green, style.Green, true
		}
		return style.Subtle, style.Muted, false
	}
	return style.Subtle, style.Muted, false
}

func drawDBox(c *canvas, b *dbox) {
	edge, text, bold := dboxColors(b.node)
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
	runes := []rune(b.node.label)
	pad := (inner - len(runes)) / 2
	for i := 0; i < inner; i++ {
		c.set(l+1+i, t+1, scell{r: ' '})
	}
	for i, rr := range runes {
		c.set(l+1+pad+i, t+1, scell{r: rr, fg: text, bold: bold})
	}
}

// drawVEdge connects a parent to its single child with a vertical line, an
// upward arrowhead below the parent, and — for a labeled edge — the label
// centered on the lane (as in "document:roadmap →owner from→ …#owner").
func drawVEdge(c *canvas, parent, kid *dbox) {
	col := connColor(kid.node)
	x := parent.cx
	top, bottom := parent.y+dbBoxH, kid.y-1
	for y := top; y <= bottom; y++ {
		c.set(x, y, scell{r: '│', fg: col})
	}
	c.set(x, top, scell{r: '↑', fg: col})
	if kid.node.edge != "" {
		runes := []rune(kid.node.edge)
		ly := top + (bottom-top+1)/2
		lx := x - len(runes)/2
		for i, rr := range runes {
			c.set(lx+i, ly, scell{r: rr, fg: style.Muted})
		}
	}
}

// drawBus connects a parent to several children through a horizontal bus, one
// row below the parent, dropping into each child's top border.
func drawBus(c *canvas, b *dbox) {
	px := b.cx
	busY := b.y + dbBoxH
	centers := make([]int, len(b.kids))
	minC, maxC := px, px
	for i, k := range b.kids {
		centers[i] = k.cx
		minC, maxC = min(minC, k.cx), max(maxC, k.cx)
	}
	pcol := connColor(b.node)
	c.set(px, b.y+2, scell{r: '┬', fg: pcol})
	for x := minC; x <= maxC; x++ {
		up := x == px
		down := slices.Contains(centers, x)
		c.set(x, busY, scell{r: busRune(up, down, x > minC, x < maxC), fg: pcol})
	}
	for i, k := range b.kids {
		c.set(centers[i], k.y, scell{r: '┴', fg: connColor(k.node)})
	}
}

// connColor tints a connector: green when it leads to a granted node, otherwise
// subtle.
func connColor(n *dispNode) color.Color {
	if n.granted {
		return style.Green
	}
	return style.Subtle
}

// busRune picks the box-drawing glyph for a bus cell from which sides it links to.
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
