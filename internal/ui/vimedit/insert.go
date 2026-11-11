package vimedit

import (
	"slices"
	"strings"
)

// nestIndent returns the indentation a new line below text gets: the line's
// own leading blanks, plus one indent unit for DSL nesting openers.
func nestIndent(lead []rune, text string, below bool) []rune {
	ind := slices.Clone(lead)
	if below {
		t := strings.TrimSpace(text)
		if t == "model" || t == "relations" || strings.HasPrefix(strings.TrimLeft(text, " \t"), "type ") {
			ind = append(ind, ' ', ' ')
		}
	}
	return ind
}

func (e *Editor) enterInsert() {
	e.mode = Insert
	e.didChange = true
}

func (e *Editor) insertKey(k Key) Action {
	switch k.Name {
	case "esc":
		e.mode = Normal
		e.recIns = false
		if e.col > 0 {
			e.col--
		}
		e.want = e.col
		return None
	case "enter":
		e.insertNewline()
	case "tab":
		e.insertRunes([]rune("  "))
	case "backspace":
		e.insertBackspace()
	case "delete":
		e.insertDelete()
	case "ctrl+w":
		e.insertDeleteWord()
	case "ctrl+u":
		e.insertDeleteToStart()
	case "left", "right", "up", "down", "home", "end", "pgup", "pgdown":
		e.insertMove(k.Name)
		return None
	case pasteKey:
		end := e.insertAt(Pos{e.row, e.col}, k.Text)
		e.row, e.col, e.want = end.Row, end.Col, end.Col
	default:
		if k.Text == "" {
			return None
		}
		e.insertRunes([]rune(k.Text))
		e.recordIns(Key{Text: k.Text})
		return None
	}
	e.recordIns(k)
	return None
}

func (e *Editor) insertRunes(add []rune) {
	l := e.lines[e.row]
	e.setLine(e.row, slices.Concat(l[:e.col], add, l[e.col:]))
	e.col += len(add)
}

func (e *Editor) insertNewline() {
	l := e.lines[e.row]
	before, after := l[:e.col], l[e.col:]
	ind := nestIndent(l[:e.indentLen(e.row)], string(before), true)
	i := 0
	for i < len(after) && isBlank(after[i]) {
		i++
	}
	nl := slices.Concat(ind, after[i:])
	e.splice(e.row, e.row, [][]rune{slices.Clone(before), nl})
	e.row++
	e.col = len(ind)
}

func (e *Editor) insertBackspace() {
	switch {
	case e.col > 0:
		l := e.lines[e.row]
		e.setLine(e.row, slices.Concat(l[:e.col-1], l[e.col:]))
		e.col--
	case e.row > 0:
		prev := e.lines[e.row-1]
		e.splice(e.row-1, e.row, [][]rune{slices.Concat(prev, e.lines[e.row])})
		e.row--
		e.col = len(prev)
	}
}

func (e *Editor) insertDelete() {
	l := e.lines[e.row]
	switch {
	case e.col < len(l):
		e.setLine(e.row, slices.Concat(l[:e.col], l[e.col+1:]))
	case e.row < e.lastRow():
		e.splice(e.row, e.row+1, [][]rune{slices.Concat(l, e.lines[e.row+1])})
	}
}

func (e *Editor) insertDeleteWord() {
	if e.col == 0 {
		e.insertBackspace()
		return
	}
	l := e.lines[e.row]
	c := e.col
	for c > 0 && isBlank(l[c-1]) {
		c--
	}
	if c > 0 {
		cl := cls(l[c-1], false)
		for c > 0 && cls(l[c-1], false) == cl {
			c--
		}
	}
	e.setLine(e.row, slices.Concat(l[:c], l[e.col:]))
	e.col = c
}

func (e *Editor) insertDeleteToStart() {
	if e.col == 0 {
		return
	}
	l := e.lines[e.row]
	to := 0
	if ind := e.indentLen(e.row); e.col > ind {
		to = ind
	}
	e.setLine(e.row, slices.Concat(l[:to], l[e.col:]))
	e.col = to
}

func (e *Editor) insertMove(name string) {
	e.snapped = false
	e.recIns = false
	l := e.lines[e.row]
	switch name {
	case "left":
		e.col = max(e.col-1, 0)
	case "right":
		e.col = min(e.col+1, len(l))
	case "home":
		e.col = 0
	case "end":
		e.col = len(l)
	case "up", "down", "pgup", "pgdown":
		n := 1
		if name == "pgup" || name == "pgdown" {
			n = e.vpHeight
		}
		if name == "up" || name == "pgup" {
			n = -n
		}
		e.row = max(0, min(e.row+n, e.lastRow()))
		if e.want < 0 {
			e.col = len(e.lines[e.row])
		} else {
			e.col = min(e.want, len(e.lines[e.row]))
		}
		e.keepWant = true
		return
	}
	e.want = e.col
	e.keepWant = true
}
