package vimedit

import (
	"unicode"
)

// mkind says how a motion's range is interpreted by an operator.
type mkind int

const (
	excl mkind = iota
	incl
	linew
)

// mres is the result of a motion: where it lands and how to treat the range.
type mres struct {
	p    Pos
	kind mkind
	vert bool // vertical: column comes from the desired column
	fnb  bool // land on the first non-blank of the row
	eol  bool // "$": desired column becomes end-of-line
}

// cls is the character class used by word motions: 0 blank, 1 word, 2 punct.
func cls(r rune, big bool) int {
	switch {
	case r == ' ' || r == '\t':
		return 0
	case big:
		return 1
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

func isWordRune(r rune) bool { return cls(r, false) == 1 }

func (e *Editor) wordFwd(p Pos, big bool) Pos {
	r, c := p.Row, p.Col
	l := e.lines[r]
	if c < len(l) {
		if cl := cls(l[c], big); cl != 0 {
			for c < len(l) && cls(l[c], big) == cl {
				c++
			}
		}
	}
	for {
		l = e.lines[r]
		for c < len(l) && cls(l[c], big) == 0 {
			c++
		}
		if c < len(l) {
			return Pos{r, c}
		}
		if r == e.lastRow() {
			return Pos{r, len(l)}
		}
		r++
		c = 0
		if len(e.lines[r]) == 0 {
			return Pos{r, 0}
		}
	}
}

func (e *Editor) wordBack(p Pos, big bool) Pos {
	r, c := p.Row, p.Col
	step := func() bool {
		if c > 0 {
			c--
			return true
		}
		if r == 0 {
			return false
		}
		r--
		c = len(e.lines[r])
		if c > 0 {
			c--
		}
		return true
	}
	if !step() {
		return Pos{0, 0}
	}
	for {
		l := e.lines[r]
		if len(l) == 0 {
			return Pos{r, 0}
		}
		if c < len(l) && cls(l[c], big) != 0 {
			break
		}
		if !step() {
			return Pos{0, 0}
		}
	}
	l := e.lines[r]
	cl := cls(l[c], big)
	for c > 0 && cls(l[c-1], big) == cl {
		c--
	}
	return Pos{r, c}
}

func (e *Editor) wordEnd(p Pos, big bool) Pos {
	r, c := p.Row, p.Col+1
	for {
		l := e.lines[r]
		for c < len(l) && cls(l[c], big) == 0 {
			c++
		}
		if c < len(l) {
			break
		}
		if r == e.lastRow() {
			return Pos{r, max(len(l)-1, 0)}
		}
		r++
		c = 0
	}
	l := e.lines[r]
	cl := cls(l[c], big)
	for c+1 < len(l) && cls(l[c+1], big) == cl {
		c++
	}
	return Pos{r, c}
}

// cwEnd implements the `cw` special case: change to the end of the word.
func (e *Editor) cwEnd(cnt int, big bool) mres {
	l := e.lines[e.row]
	c := e.col
	cl := cls(l[c], big)
	for c+1 < len(l) && cls(l[c+1], big) == cl {
		c++
	}
	p := Pos{e.row, c}
	for i := 1; i < cnt; i++ {
		p = e.wordEnd(p, big)
	}
	return mres{p: p, kind: incl}
}

func (e *Editor) findChar(kind, ch rune, cnt int, repeat bool) (int, bool) {
	l := e.lines[e.row]
	fwd := kind == 'f' || kind == 't'
	step := 1
	if !fwd {
		step = -1
	}
	till := kind == 't' || kind == 'T'
	pos := e.col
	for i := 0; i < cnt; i++ {
		j := pos + step
		if i == 0 && repeat && till && j >= 0 && j < len(l) && l[j] == ch {
			j += step
		}
		for j >= 0 && j < len(l) && l[j] != ch {
			j += step
		}
		if j < 0 || j >= len(l) {
			return 0, false
		}
		pos = j
	}
	if till {
		pos -= step
	}
	return pos, true
}

func (e *Editor) matchPair() (Pos, bool) {
	const pairs = "()[]{}"
	l := e.lines[e.row]
	c := e.col
	idx := -1
	for ; c < len(l); c++ {
		if i := indexRune(pairs, l[c]); i >= 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Pos{}, false
	}
	ch := l[c]
	other := rune(pairs[idx^1])
	fwd := idx%2 == 0
	depth := 0
	r := e.row
	if fwd {
		for {
			l = e.lines[r]
			for j := c + 1; j < len(l); j++ {
				switch l[j] {
				case ch:
					depth++
				case other:
					if depth == 0 {
						return Pos{r, j}, true
					}
					depth--
				}
			}
			r++
			c = -1
			if r > e.lastRow() {
				return Pos{}, false
			}
		}
	}
	for {
		l = e.lines[r]
		start := c - 1
		if start >= len(l) {
			start = len(l) - 1
		}
		for j := start; j >= 0; j-- {
			switch l[j] {
			case ch:
				depth++
			case other:
				if depth == 0 {
					return Pos{r, j}, true
				}
				depth--
			}
		}
		r--
		if r < 0 {
			return Pos{}, false
		}
		c = len(e.lines[r])
	}
}

func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

func (e *Editor) paraFwd(cnt int) Pos {
	r := e.row
	last := e.lastRow()
	for i := 0; i < cnt; i++ {
		for r < last && len(e.lines[r]) == 0 {
			r++
		}
		for r < last && len(e.lines[r]) != 0 {
			r++
		}
	}
	if r == last && len(e.lines[r]) != 0 {
		return Pos{r, len(e.lines[r])}
	}
	return Pos{r, 0}
}

func (e *Editor) paraBack(cnt int) Pos {
	r := e.row
	for i := 0; i < cnt; i++ {
		if r > 0 {
			r--
		}
		for r > 0 && len(e.lines[r]) == 0 {
			r--
		}
		for r > 0 && len(e.lines[r]) != 0 {
			r--
		}
	}
	return Pos{r, 0}
}

// motion computes a motion. ok is false when the motion fails (which cancels
// a pending operator).
func (e *Editor) motion(key string, ch rune, cnt int, has, hasOp bool) (mres, bool) {
	l := e.lines[e.row]
	last := e.lastRow()
	switch key {
	case "h", "left", "backspace":
		if e.col == 0 {
			return mres{}, false
		}
		return mres{p: Pos{e.row, max(e.col-cnt, 0)}}, true
	case "l", "right", "space":
		hi := len(l) - 1
		if hasOp {
			hi = len(l)
		}
		if e.col >= hi {
			return mres{}, false
		}
		return mres{p: Pos{e.row, min(e.col+cnt, hi)}}, true
	case "j", "down":
		if e.row == last {
			return mres{}, false
		}
		return mres{p: Pos{min(e.row+cnt, last), 0}, kind: linew, vert: true}, true
	case "k", "up":
		if e.row == 0 {
			return mres{}, false
		}
		return mres{p: Pos{max(e.row-cnt, 0), 0}, kind: linew, vert: true}, true
	case "ctrl+d", "ctrl+f", "pgdown":
		n := cnt * e.vpHeight
		if key == "ctrl+d" {
			n = cnt * max(e.vpHeight/2, 1)
		}
		return mres{p: Pos{min(e.row+n, last), 0}, kind: linew, vert: true}, true
	case "ctrl+u", "ctrl+b", "pgup":
		n := cnt * e.vpHeight
		if key == "ctrl+u" {
			n = cnt * max(e.vpHeight/2, 1)
		}
		return mres{p: Pos{max(e.row-n, 0), 0}, kind: linew, vert: true}, true
	case "w", "W":
		p := e.cursorPos()
		for i := 0; i < cnt; i++ {
			p = e.wordFwd(p, key == "W")
		}
		if hasOp && p.Row > e.row && p.Col <= e.indentLen(p.Row) {
			if p.Row-1 == e.row && len(l) == 0 && e.op != "c" {
				// vim: dw on an empty line deletes its line break.
				p = Pos{e.row + 1, 0}
			} else {
				p = Pos{p.Row - 1, len(e.lines[p.Row-1])}
			}
		}
		return mres{p: p}, true
	case "b", "B":
		p := e.cursorPos()
		if p == (Pos{}) {
			return mres{}, false
		}
		for i := 0; i < cnt; i++ {
			p = e.wordBack(p, key == "B")
		}
		return mres{p: p}, true
	case "e", "E":
		p := e.cursorPos()
		for i := 0; i < cnt; i++ {
			p = e.wordEnd(p, key == "E")
		}
		return mres{p: p, kind: incl}, true
	case "0", "home":
		return mres{p: Pos{e.row, 0}}, true
	case "^":
		return mres{p: Pos{e.row, e.firstNonBlank(e.row)}}, true
	case "$", "end":
		r := min(e.row+cnt-1, last)
		return mres{p: Pos{r, max(len(e.lines[r])-1, 0)}, kind: incl, eol: true}, true
	case "gg":
		r := 0
		if has {
			r = min(cnt-1, last)
		}
		return mres{p: Pos{r, 0}, kind: linew, fnb: true}, true
	case "G":
		r := last
		if has {
			r = min(cnt-1, last)
		}
		return mres{p: Pos{r, 0}, kind: linew, fnb: true}, true
	case "f", "F", "t", "T":
		e.lastFindKind, e.lastFindCh = rune(key[0]), ch
		return e.doFind(rune(key[0]), ch, cnt, false)
	case ";", ",":
		if e.lastFindKind == 0 {
			return mres{}, false
		}
		kind := e.lastFindKind
		if key == "," {
			kind = swapFind(kind)
		}
		return e.doFind(kind, e.lastFindCh, cnt, true)
	case "%":
		p, ok := e.matchPair()
		return mres{p: p, kind: incl}, ok
	case "}":
		return mres{p: e.paraFwd(cnt)}, true
	case "{":
		return mres{p: e.paraBack(cnt)}, true
	case "H", "M", "L":
		top := min(e.top, last)
		bot := min(top+e.vpHeight-1, last)
		var r int
		switch key {
		case "H":
			r = min(top+cnt-1, bot)
		case "L":
			r = max(bot-cnt+1, top)
		default:
			r = top + (bot-top)/2
		}
		return mres{p: Pos{r, 0}, kind: linew, fnb: true}, true
	case "n", "N", "*", "#":
		return e.searchMotion(key, cnt)
	}
	return mres{}, false
}

func swapFind(k rune) rune {
	switch k {
	case 'f':
		return 'F'
	case 'F':
		return 'f'
	case 't':
		return 'T'
	}
	return 't'
}

func (e *Editor) doFind(kind, ch rune, cnt int, repeat bool) (mres, bool) {
	c, ok := e.findChar(kind, ch, cnt, repeat)
	if !ok {
		return mres{}, false
	}
	k := excl
	if kind == 'f' || kind == 't' {
		k = incl
	}
	return mres{p: Pos{e.row, c}, kind: k}, true
}

func (e *Editor) cursorPos() Pos { return Pos{e.row, e.col} }

// isMotionKey reports whether key names a motion (so a pending operator can
// take it as its target).
func isMotionKey(key string) bool {
	switch key {
	case "h", "left", "backspace", "l", "right", "space", "j", "down", "k", "up",
		"ctrl+d", "ctrl+f", "pgdown", "ctrl+u", "ctrl+b", "pgup",
		"w", "W", "b", "B", "e", "E", "0", "home", "^", "$", "end", "G",
		";", ",", "%", "}", "{", "H", "M", "L", "n", "N", "*", "#":
		return true
	}
	return false
}
