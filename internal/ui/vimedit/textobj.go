package vimedit

// objRange is a text object's extent: [a, b) charwise, or rows a.Row..b.Row
// inclusive when lines is set.
type objRange struct {
	a, b  Pos
	lines bool
}

// textObject resolves a text object like "iw" or "a(" at the cursor. cnt
// extends a word object over that many words, and picks the cnt-th enclosing
// pair of a bracket object.
func (e *Editor) textObject(inner bool, obj rune, cnt int) (objRange, bool) {
	switch obj {
	case 'w', 'W':
		return e.objWord(inner, obj == 'W', cnt)
	case '(', ')', 'b':
		return e.objPair(inner, '(', ')', cnt)
	case '[', ']':
		return e.objPair(inner, '[', ']', cnt)
	case '{', '}', 'B':
		return e.objPair(inner, '{', '}', cnt)
	case '"', '\'':
		return e.objQuote(inner, obj)
	}
	return objRange{}, false
}

func (e *Editor) objWord(inner, big bool, cnt int) (objRange, bool) {
	l := e.lines[e.row]
	if len(l) == 0 {
		return objRange{a: Pos{e.row, 0}, b: Pos{e.row, 0}}, true
	}
	c := min(e.col, len(l)-1)
	cl := cls(l[c], big)
	onBlank := cl == 0
	s, t := c, c
	for s > 0 && cls(l[s-1], big) == cl {
		s--
	}
	for t+1 < len(l) && cls(l[t+1], big) == cl {
		t++
	}
	end := t + 1
	switch {
	case inner:
	case cl != 0:
		u := end
		for u < len(l) && cls(l[u], big) == 0 {
			u++
		}
		if u > end {
			end = u
		} else {
			for s > 0 && cls(l[s-1], big) == 0 {
				s--
			}
		}
	default:
		if end < len(l) {
			cl2 := cls(l[end], big)
			for end < len(l) && cls(l[end], big) == cl2 {
				end++
			}
		}
	}
	// Each further count takes the next word (iw: the next word or blank run
	// in turn; aw: the next word and the blanks after it, or, started on
	// blanks, the blanks and the word after them), on this line.
	for range cnt - 1 {
		if !inner && onBlank {
			for end < len(l) && cls(l[end], big) == 0 {
				end++
			}
		}
		if end >= len(l) {
			return objRange{}, false
		}
		cl := cls(l[end], big)
		for end < len(l) && cls(l[end], big) == cl {
			end++
		}
		if !inner && cl != 0 && !onBlank {
			for end < len(l) && cls(l[end], big) == 0 {
				end++
			}
		}
	}
	return objRange{a: Pos{e.row, s}, b: Pos{e.row, end}}, true
}

// openBefore finds the unmatched open bracket before from on or above its row.
func (e *Editor) openBefore(from Pos, open, closeCh rune) (Pos, bool) {
	depth := 0
	r, c := from.Row, from.Col-1
	for {
		ln := e.lines[r]
		if c >= len(ln) {
			c = len(ln) - 1
		}
		for ; c >= 0; c-- {
			switch ln[c] {
			case closeCh:
				depth++
			case open:
				if depth == 0 {
					return Pos{r, c}, true
				}
				depth--
			}
		}
		r--
		if r < 0 {
			return Pos{}, false
		}
		c = len(e.lines[r]) - 1
	}
}

func (e *Editor) objPair(inner bool, open, closeCh rune, cnt int) (objRange, bool) {
	l := e.lines[e.row]
	op, found := Pos{e.row, e.col}, e.col < len(l) && l[e.col] == open
	if !found {
		op, found = e.openBefore(e.cursorPos(), open, closeCh)
	}
	for i := 1; found && i < cnt; i++ {
		op, found = e.openBefore(op, open, closeCh)
	}
	if !found {
		return objRange{}, false
	}
	cp, ok := e.scanClose(op, open, closeCh)
	if !ok {
		return objRange{}, false
	}
	if !inner {
		return objRange{a: op, b: Pos{cp.Row, cp.Col + 1}}, true
	}
	a, b := Pos{op.Row, op.Col + 1}, cp
	if cp.Row > op.Row {
		openLast := op.Col+1 >= len(e.lines[op.Row])
		closeFirst := e.indentLen(cp.Row) >= cp.Col
		if openLast && closeFirst {
			if cp.Row-1 >= op.Row+1 {
				return objRange{a: Pos{op.Row + 1, 0}, b: Pos{cp.Row - 1, 0}, lines: true}, true
			}
			return objRange{a: Pos{op.Row, len(e.lines[op.Row])}, b: Pos{op.Row, len(e.lines[op.Row])}}, true
		}
	}
	return objRange{a: a, b: b}, true
}

func (e *Editor) scanClose(op Pos, open, closeCh rune) (Pos, bool) {
	depth := 0
	r, c := op.Row, op.Col+1
	for r <= e.lastRow() {
		l := e.lines[r]
		for ; c < len(l); c++ {
			switch l[c] {
			case open:
				depth++
			case closeCh:
				if depth == 0 {
					return Pos{r, c}, true
				}
				depth--
			}
		}
		r++
		c = 0
	}
	return Pos{}, false
}

func (e *Editor) objQuote(inner bool, q rune) (objRange, bool) {
	l := e.lines[e.row]
	var pairs [][2]int
	open := -1
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '\\':
			i++
		case q:
			if open < 0 {
				open = i
			} else {
				pairs = append(pairs, [2]int{open, i})
				open = -1
			}
		}
	}
	for _, p := range pairs {
		if e.col <= p[1] {
			s, t := p[0], p[1]
			if inner {
				return objRange{a: Pos{e.row, s + 1}, b: Pos{e.row, t}}, true
			}
			end := t + 1
			for end < len(l) && isBlank(l[end]) {
				end++
			}
			if end == t+1 {
				for s > 0 && isBlank(l[s-1]) {
					s--
				}
			}
			return objRange{a: Pos{e.row, s}, b: Pos{e.row, end}}, true
		}
	}
	return objRange{}, false
}
