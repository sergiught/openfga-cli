package vimedit

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
)

func (e *Editor) inVisual() bool { return e.mode == Visual || e.mode == VisualLine }

// count returns the combined count (before and after the operator).
func (e *Editor) count() (int, bool) {
	c, has := 1, false
	if e.n1 > 0 {
		c *= e.n1
		has = true
	}
	if e.n2 > 0 {
		c *= e.n2
		has = true
	}
	return min(c, maxCount), has
}

func (e *Editor) cancel() {
	e.didChange = false
	e.clearPending()
}

// endCmd completes the current command, recording it for dot-repeat.
func (e *Editor) endCmd() {
	if e.didChange && !e.replaying && !e.inVisual() {
		cnt, has := e.count()
		if !has {
			cnt = 0
		}
		e.dot = dotRec{count: cnt, keys: slices.Clone(e.cmdKeys)}
		e.recIns = e.mode == Insert
	}
	e.cancel()
}

func keyRune(k Key) rune {
	if r := []rune(k.Text); len(r) == 1 {
		return r[0]
	}
	if r := []rune(k.Name); len(r) == 1 {
		return r[0]
	}
	if k.Name == "space" {
		return ' '
	}
	return 0
}

func (e *Editor) normalKey(k Key) Action {
	key := k.Name
	visual := e.inVisual()

	if key == "esc" {
		if visual {
			e.exitVisual()
			e.cancel()
			return None
		}
		had := e.pend != ""
		e.cancel()
		if had {
			return None
		}
		return Close
	}
	e.pend += key

	if e.wait == "\"" {
		e.wait = ""
		return None
	}
	if e.wait != "" {
		w := e.wait
		e.wait = ""
		e.cmdKeys = append(e.cmdKeys, k)
		e.waitedKey(w, k)
		return None
	}

	if len(key) == 1 && (key[0] >= '1' && key[0] <= '9' || key[0] == '0' && e.countActive()) {
		d := int(key[0] - '0')
		if e.op == "" {
			e.n1 = min(e.n1*10+d, maxCount)
		} else {
			e.n2 = min(e.n2*10+d, maxCount)
		}
		return None
	}
	e.cmdKeys = append(e.cmdKeys, k)
	return e.dispatch(key)
}

func (e *Editor) countActive() bool {
	if e.op == "" {
		return e.n1 > 0
	}
	return e.n2 > 0
}

// waitedKey completes a two-key command whose prefix was stored in e.wait.
func (e *Editor) waitedKey(w string, k Key) {
	ch := keyRune(k)
	switch w {
	case "z":
		e.cancel()
	case "g":
		if k.Name == "g" {
			e.runMotion("gg", 0)
			return
		}
		e.cancel()
	case "f", "F", "t", "T":
		if ch == 0 {
			e.cancel()
			return
		}
		e.runMotion(w, ch)
	case "r":
		if ch == 0 {
			e.cancel()
			return
		}
		e.replaceChars(ch)
	case "i", "a":
		e.runObject(w == "i", ch)
	}
}

func (e *Editor) dispatch(key string) Action {
	visual := e.inVisual()
	cnt, _ := e.count()
	switch key {
	case "\"":
		e.wait = "\""
		return None
	case "i", "a":
		if e.op != "" || visual {
			e.wait = key
			return None
		}
	case "g", "f", "F", "t", "T", "z":
		e.wait = key
		return None
	case "r":
		if e.op == "" {
			e.wait = "r"
			return None
		}
	case "d", "c", "y", ">", "<":
		switch {
		case visual:
			e.visualOp(key, cnt)
		case e.op == "":
			e.op, e.n2 = key, 0
		case e.op == key:
			e.lineOp(cnt)
			e.endCmd()
		default:
			e.cancel()
		}
		return None
	}
	if isMotionKey(key) {
		e.runMotion(key, 0)
		return None
	}
	if e.op != "" {
		e.cancel()
		return None
	}
	if visual {
		e.visualCmd(key, cnt)
		return None
	}
	return e.command(key, cnt)
}

// runMotion executes a motion, as a cursor move or as an operator target.
func (e *Editor) runMotion(key string, ch rune) {
	cnt, has := e.count()
	var m mres
	var ok bool
	if e.op == "c" && (key == "w" || key == "W") && e.col < len(e.lines[e.row]) && cls(e.lines[e.row][e.col], key == "W") != 0 {
		m, ok = e.cwEnd(cnt, key == "W"), true
	} else {
		m, ok = e.motion(key, ch, cnt, has, e.op != "")
	}
	if !ok && e.op == "c" && len(e.lines[e.row]) == 0 && (key == "l" || key == "right" || key == "space") {
		// vim: `cl`, and so `s`, on an empty line changes the empty span.
		m, ok = mres{p: e.cursorPos()}, true
	}
	if !ok {
		e.cancel()
		return
	}
	if e.op == "" {
		e.moveTo(m)
		e.endCmd()
		return
	}
	e.opOnMotion(m, key)
	e.endCmd()
}

func (e *Editor) moveTo(m mres) {
	e.row = m.p.Row
	switch {
	case m.vert:
		e.col = e.colFor(m.p.Row)
		e.keepWant = true
	case m.fnb:
		e.col = e.firstNonBlank(m.p.Row)
	case m.eol:
		e.col = max(len(e.lines[m.p.Row])-1, 0)
		e.want = -1
		e.keepWant = true
	default:
		e.col = m.p.Col
	}
}

func (e *Editor) opOnMotion(m mres, key string) {
	a, b := e.cursorPos(), m.p
	if posLess(b, a) {
		a, b = b, a
	}
	switch m.kind {
	case linew:
		e.applyOp(e.op, a, b, true)
		return
	case incl:
		b.Col = min(b.Col+1, len(e.lines[b.Row]))
	default:
		if b.Col == 0 && b.Row > a.Row && key != "w" && key != "W" {
			b = Pos{b.Row - 1, len(e.lines[b.Row-1])}
			if a.Col <= e.indentLen(a.Row) {
				e.applyOp(e.op, a, b, true)
				return
			}
		}
	}
	e.applyOp(e.op, a, b, false)
}

func (e *Editor) runObject(inner bool, ch rune) {
	r, ok := e.textObject(inner, ch)
	if !ok {
		e.cancel()
		return
	}
	if e.inVisual() {
		e.selectObject(r)
		e.cancel()
		return
	}
	if r.lines {
		e.applyOp(e.op, r.a, r.b, true)
	} else {
		e.applyOp(e.op, r.a, r.b, false)
	}
	e.endCmd()
}

func (e *Editor) selectObject(r objRange) {
	if r.lines {
		e.vstart = Pos{r.a.Row, 0}
		e.row, e.col = r.b.Row, max(len(e.lines[r.b.Row])-1, 0)
		return
	}
	end := r.b
	if end.Col > 0 {
		end.Col--
	}
	e.vstart, e.row, e.col = r.a, end.Row, end.Col
}

// lineOp is the doubled operator (dd, yy, cc, >>, <<) on cnt lines.
func (e *Editor) lineOp(cnt int) {
	r2 := min(e.row+cnt-1, e.lastRow())
	e.applyOp(e.op, Pos{e.row, 0}, Pos{r2, 0}, true)
}

// applyOp applies op to [a, b) charwise, or to rows a.Row..b.Row when lw.
func (e *Editor) applyOp(op string, a, b Pos, lw bool) {
	if lw {
		e.applyLineOp(op, a.Row, b.Row)
		return
	}
	switch op {
	case "y":
		e.reg = register{text: e.textRange(a, b)}
		e.row, e.col = a.Row, a.Col
	case "d", "c":
		if a != b {
			e.reg = register{text: e.textRange(a, b)}
		}
		e.deleteRange(a, b)
		e.row, e.col = a.Row, a.Col
		e.didChange = true
		if op == "c" {
			e.enterInsert()
		}
	default:
		if b.Col == 0 && b.Row > a.Row {
			b.Row--
		}
		e.shiftRows(a.Row, b.Row, op == ">", 1)
	}
}

func (e *Editor) applyLineOp(op string, r1, r2 int) {
	n := r2 - r1 + 1
	switch op {
	case "y":
		e.reg = register{text: e.rowsText(r1, r2), linewise: true}
		e.row = r1
		if n > 2 {
			e.status = strconv.Itoa(n) + " lines yanked"
		}
	case "d":
		e.reg = register{text: e.rowsText(r1, r2), linewise: true}
		e.splice(r1, r2, nil)
		e.cursorFNB(r1)
		e.didChange = true
		if n > 2 {
			e.status = strconv.Itoa(n) + " fewer lines"
		}
	case "c":
		e.reg = register{text: e.rowsText(r1, r2), linewise: true}
		ind := slices.Clone(e.lines[r1][:e.indentLen(r1)])
		e.splice(r1, r2, [][]rune{ind})
		e.row, e.col = r1, len(ind)
		e.enterInsert()
	default:
		e.shiftRows(r1, r2, op == ">", 1)
	}
}

// shiftRows indents or dedents rows by the DSL indent unit, times times.
func (e *Editor) shiftRows(r1, r2 int, right bool, times int) {
	for r := r1; r <= r2; r++ {
		l := e.lines[r]
		if right {
			if len(l) > 0 {
				e.setLine(r, slices.Concat([]rune(strings.Repeat(" ", indentUnit*times)), l))
			}
			continue
		}
		n := 0
		for n < len(l) && n < indentUnit*times && isBlank(l[n]) {
			n++
		}
		if n > 0 {
			e.setLine(r, slices.Clone(l[n:]))
		}
	}
	e.didChange = true
	e.cursorFNB(r1)
}

// ---- non-motion normal commands ----

func (e *Editor) command(key string, cnt int) Action {
	_, has := e.count()
	switch key {
	case "x":
		e.startOp("d", "l")
	case "X":
		e.startOp("d", "h")
	case "s":
		e.startOp("c", "l")
	case "D":
		e.startOp("d", "$")
	case "C":
		e.startOp("c", "$")
	case "S":
		e.op = "c"
		e.lineOp(cnt)
		e.endCmd()
	case "Y":
		e.op = "y"
		e.lineOp(cnt)
		e.endCmd()
	case "i":
		e.enterInsert()
		e.endCmd()
	case "a":
		if len(e.lines[e.row]) > 0 {
			e.col++
		}
		e.enterInsert()
		e.endCmd()
	case "I":
		e.col = e.indentLen(e.row)
		e.enterInsert()
		e.endCmd()
	case "A":
		e.col = len(e.lines[e.row])
		e.enterInsert()
		e.endCmd()
	case "o", "O":
		l := e.lines[e.row]
		below := key == "o"
		ind := nestIndent(l[:e.indentLen(e.row)], string(l), below)
		at := e.row
		if below {
			at++
		}
		e.insertRows(at, [][]rune{ind})
		e.row, e.col = at, len(ind)
		e.enterInsert()
		e.endCmd()
	case "p", "P":
		e.put(key == "p", cnt)
		e.endCmd()
	case "J":
		e.joinRows(e.row, min(e.row+max(cnt, 2)-1, e.lastRow()))
		e.endCmd()
	case "~":
		e.toggleCase(cnt)
		e.endCmd()
	case "u":
		e.undoSteps(cnt)
		e.cancel()
	case "ctrl+r":
		e.redoSteps(cnt)
		e.cancel()
	case ".":
		e.repeatDot(cnt, has)
	case "v", "V":
		e.vstart = e.cursorPos()
		e.mode = Visual
		if key == "V" {
			e.mode = VisualLine
		}
		e.cancel()
	case ":", "/", "?":
		e.enterCommand(rune(key[0]), "")
		e.cancel()
	default:
		e.cancel()
	}
	return None
}

// startOp runs an operator alias such as x (= dl) or D (= d$).
func (e *Editor) startOp(op, motionKey string) {
	e.op, e.n2 = op, 0
	e.runMotion(motionKey, 0)
}

func (e *Editor) repeatDot(cnt int, has bool) {
	d := e.dot
	e.cancel()
	if len(d.keys) == 0 {
		return
	}
	if has {
		d.count = cnt
	}
	e.replaying = true
	defer func() { e.replaying = false }()
	if d.count > 0 {
		for _, c := range strconv.Itoa(d.count) {
			e.normalKey(Key{Name: string(c), Text: string(c)})
		}
	}
	for _, k := range d.keys {
		e.normalKey(k)
	}
	if e.mode == Insert {
		for _, k := range d.ins {
			e.insertKey(k)
		}
		e.insertKey(Key{Name: "esc"})
	}
	e.cancel()
	e.snapped = false
}

func (e *Editor) replaceChars(ch rune) {
	if e.inVisual() {
		e.dot = dotRec{}
		e.visualReplace(ch)
		e.cancel()
		return
	}
	cnt, _ := e.count()
	l := e.lines[e.row]
	if e.col+cnt > len(l) {
		e.cancel()
		return
	}
	nl := slices.Clone(l)
	for i := 0; i < cnt; i++ {
		nl[e.col+i] = ch
	}
	e.setLine(e.row, nl)
	e.col += cnt - 1
	e.didChange = true
	e.endCmd()
}

func (e *Editor) put(after bool, cnt int) {
	r := e.reg
	if r.text == "" && !r.linewise {
		return
	}
	e.didChange = true
	if r.linewise {
		var rows [][]rune
		for i := 0; i < cnt; i++ {
			for _, ln := range strings.Split(r.text, "\n") {
				rows = append(rows, []rune(ln))
			}
		}
		at := e.row
		if after {
			at++
		}
		e.insertRows(at, rows)
		e.cursorFNB(at)
		return
	}
	txt := strings.Repeat(r.text, cnt)
	at := e.cursorPos()
	if after && len(e.lines[e.row]) > 0 {
		at.Col++
	}
	end := e.insertAt(at, txt)
	if strings.Contains(txt, "\n") {
		e.row, e.col = at.Row, at.Col
	} else {
		e.row, e.col = end.Row, end.Col-1
	}
}

// joinRows joins rows r1..r2 like vim's J.
func (e *Editor) joinRows(r1, r2 int) {
	if r2 <= r1 {
		return
	}
	cur := slices.Clone(e.lines[r1])
	col := 0
	for r := r1 + 1; r <= r2; r++ {
		next := e.lines[r]
		i := 0
		for i < len(next) && isBlank(next[i]) {
			i++
		}
		next = next[i:]
		col = len(cur)
		if len(next) > 0 && len(cur) > 0 && !isBlank(cur[len(cur)-1]) && next[0] != ')' {
			cur = append(cur, ' ')
		}
		cur = append(cur, next...)
	}
	e.splice(r1, r2, [][]rune{cur})
	e.row, e.col = r1, col
	e.didChange = true
}

func toggleRune(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}

func (e *Editor) toggleCase(cnt int) {
	l := e.lines[e.row]
	if len(l) == 0 {
		return
	}
	end := min(e.col+cnt, len(l))
	e.mapRange(Pos{e.row, e.col}, Pos{e.row, end}, toggleRune)
	e.col = end
	e.didChange = true
}

// mapRange rewrites every rune in [a, b) with fn.
func (e *Editor) mapRange(a, b Pos, fn func(rune) rune) {
	for r := a.Row; r <= b.Row; r++ {
		l := e.lines[r]
		lo, hi := 0, len(l)
		if r == a.Row {
			lo = a.Col
		}
		if r == b.Row {
			hi = b.Col
		}
		var nl []rune
		for i := lo; i < hi; i++ {
			if n := fn(l[i]); n != l[i] {
				if nl == nil {
					nl = slices.Clone(l)
				}
				nl[i] = n
			}
		}
		if nl != nil {
			e.setLine(r, nl)
		}
	}
}

// ---- visual mode ----

func (e *Editor) exitVisual() {
	if sel, ok := e.Selection(); ok {
		e.visRows = [2]int{sel.Start.Row, sel.End.Row}
		e.hasVis = true
	}
	e.mode = Normal
}

// selectionRange returns the selection as [a, b) charwise (or rows when
// linewise), leaving visual mode.
func (e *Editor) selectionRange() (a, b Pos, lw bool) {
	sel, _ := e.Selection()
	e.exitVisual()
	a, b, lw = sel.Start, sel.End, sel.Linewise
	if lw {
		return
	}
	if b.Col >= len(e.lines[b.Row]) {
		if b.Row < e.lastRow() {
			return a, Pos{b.Row + 1, 0}, false
		}
		return a, Pos{b.Row, len(e.lines[b.Row])}, false
	}
	b.Col++
	return
}

func (e *Editor) visualCmd(key string, cnt int) {
	switch key {
	case "o":
		e.vstart, e.row, e.col = e.cursorPos(), e.vstart.Row, e.vstart.Col
	case "v", "V":
		want := Visual
		if key == "V" {
			want = VisualLine
		}
		if e.mode == want {
			e.exitVisual()
		} else {
			e.mode = want
		}
	case "x", "s":
		e.visualOp(map[string]string{"x": "d", "s": "c"}[key], cnt)
		return
	case "J":
		e.dot = dotRec{}
		sel, _ := e.Selection()
		e.exitVisual()
		e.joinRows(sel.Start.Row, max(sel.End.Row, min(sel.Start.Row+1, e.lastRow())))
	case "~", "u", "U":
		e.dot = dotRec{}
		a, b, lw := e.selectionRange()
		if lw {
			a.Col, b = 0, Pos{b.Row, len(e.lines[b.Row])}
		}
		fn := toggleRune
		switch key {
		case "u":
			fn = unicode.ToLower
		case "U":
			fn = unicode.ToUpper
		}
		e.mapRange(a, b, fn)
		e.row, e.col = a.Row, a.Col
	case "p", "P":
		e.dot = dotRec{}
		e.visualPut()
	case ":":
		e.exitVisual()
		e.enterCommand(':', "'<,'>")
	case "/", "?":
		e.exitVisual()
		e.enterCommand(rune(key[0]), "")
	default:
		e.cancel()
		return
	}
	e.cancel()
}

func (e *Editor) visualOp(op string, cnt int) {
	sel, _ := e.Selection()
	a, b, lw := e.selectionRange()
	switch op {
	case ">", "<":
		e.shiftRows(sel.Start.Row, sel.End.Row, op == ">", cnt)
	case "y":
		e.applyOp("y", a, b, lw)
		if lw {
			e.col = min(a.Col, max(len(e.lines[a.Row])-1, 0))
		}
	default:
		e.applyOp(op, a, b, lw)
	}
	if op != "y" {
		e.setVisualDot(op, sel)
	}
	e.cancel()
}

// setVisualDot makes `.` after a visual operator repeat it from the cursor
// over as many lines, or as many characters of one line, as vim does. Other
// selections clear it, so `.` never replays an older, unrelated change.
func (e *Editor) setVisualDot(op string, sel Selection) {
	if e.replaying {
		return
	}
	opKey := Key{Name: op, Text: op}
	switch {
	case sel.Linewise:
		e.dot = dotRec{count: sel.End.Row - sel.Start.Row + 1, keys: []Key{opKey, opKey}}
	case sel.Start.Row == sel.End.Row:
		e.dot = dotRec{count: sel.End.Col - sel.Start.Col + 1, keys: []Key{opKey, {Name: "l", Text: "l"}}}
	default:
		e.dot = dotRec{}
	}
	e.recIns = e.mode == Insert
}

func (e *Editor) visualReplace(ch rune) {
	a, b, lw := e.selectionRange()
	if lw {
		a.Col, b = 0, Pos{b.Row, len(e.lines[b.Row])}
	}
	e.mapRange(a, b, func(rune) rune { return ch })
	e.row, e.col = a.Row, a.Col
}

func (e *Editor) visualPut() {
	r := e.reg
	a, b, lw := e.selectionRange()
	old := register{linewise: lw}
	whole := lw && a.Row == 0 && b.Row == e.lastRow()
	if lw {
		old.text = e.rowsText(a.Row, b.Row)
		e.splice(a.Row, b.Row, nil)
	} else {
		old.text = e.textRange(a, b)
		e.deleteRange(a, b)
	}
	if r.text != "" || r.linewise {
		e.putReplacing(r, a, lw, whole)
	}
	e.reg = old
}

// putReplacing inserts r where the selection was removed.
func (e *Editor) putReplacing(r register, a Pos, wasLinewise, whole bool) {
	switch {
	case whole:
		// Whole buffer was replaced: overwrite the single empty line.
		e.splice(0, 0, toRows(r.text))
		e.cursorFNB(0)
	case wasLinewise:
		at := min(a.Row, len(e.lines))
		e.insertRows(at, toRows(r.text))
		e.cursorFNB(at)
	case r.linewise:
		e.insertAt(a, "\n"+r.text+"\n")
		e.cursorFNB(a.Row + 1)
	default:
		end := e.insertAt(a, r.text)
		e.row, e.col = end.Row, max(end.Col-1, 0)
	}
	e.didChange = false
}

func toRows(s string) [][]rune {
	parts := strings.Split(s, "\n")
	rows := make([][]rune, len(parts))
	for i, p := range parts {
		rows[i] = []rune(p)
	}
	return rows
}
