// Package vimedit is a vim-style line-buffer editing engine for the playground
// model editor. It has no rendering or UI dependencies: the host feeds keys in
// and reads the buffer, cursor, mode and status back out.
//
// The buffer is a slice of lines, each an immutable []rune (copy-on-write), so
// an undo snapshot is a shallow copy of the outer slice and every keypress
// costs O(line length) or O(lines) pointer work, never a join/split of the
// whole text.
package vimedit

import (
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxCount   = 10000
	maxUndo    = 500
	indentUnit = 2
)

// Mode is the editor's current input mode.
type Mode int

// The editor modes. Insert is the zero value: a new editor opens in Insert.
const (
	Insert Mode = iota
	Normal
	Visual
	VisualLine
	Command // ":" ex line, or "/" "?" search line
)

// String returns the status-line label of the mode.
func (m Mode) String() string {
	switch m {
	case Insert:
		return "INSERT"
	case Normal:
		return "NORMAL"
	case Visual:
		return "VISUAL"
	case VisualLine:
		return "V-LINE"
	case Command:
		return "COMMAND"
	}
	return "UNKNOWN"
}

// Action tells the host what to do after a key was handled.
type Action int

// The actions a key can request from the host.
const (
	None       Action = iota
	Apply             // :w, :wq, :x — host validates and applies
	Close             // :q, or Esc in Normal mode with nothing pending
	ForceClose        // :q! — close without confirming
)

// Key is one key press, mirroring tea.KeyPressMsg.
type Key struct {
	Name string // e.g. "a", "ctrl+d", "esc", "enter", "space", "backspace"
	Text string // printable text, "" for control and named keys
}

// Pos is a buffer position in rune indices.
type Pos struct{ Row, Col int }

// Selection is the Visual-mode selection, ordered (Start <= End). End is
// inclusive in charwise mode; in linewise mode only the rows matter.
type Selection struct {
	Start, End Pos
	Linewise   bool
}

type snapshot struct {
	lines    [][]rune
	row, col int
}

type register struct {
	text     string
	linewise bool
}

// pasteKey names the Insert-mode key a bracketed paste is recorded as, so
// that `.` replays it with its line breaks.
const pasteKey = "paste"

// dotRec is a repeatable change: the command keys (counts stripped), the
// combined count, and the keys typed in the Insert session it opened.
type dotRec struct {
	count int
	keys  []Key
	ins   []Key
}

func (e *Editor) clearPending() {
	e.n1, e.n2, e.op, e.wait, e.pend = 0, 0, "", "", ""
	e.cmdKeys = e.cmdKeys[:0]
}

// Editor is the editing engine. Create it with New; methods take a pointer
// receiver but the type itself is safe to store by value.
type Editor struct {
	lines    [][]rune
	row, col int
	want     int // desired column for vertical motions; -1 = end of line
	keepWant bool
	mode     Mode
	rev      uint64

	cache    string
	cacheRev uint64
	cacheOK  bool

	undo, redo []snapshot
	snapped    bool // a snapshot was already taken for the current change group

	vpHeight, top int
	reg           register
	status        string

	// Pending normal-mode command.
	n1, n2  int
	op      string
	wait    string
	cmdKeys []Key
	pend    string

	lastFindKind rune
	lastFindCh   rune
	search       string
	searchBack   bool
	searchWord   bool // search matches whole words only (from * and #)

	vstart    Pos
	visRows   [2]int
	hasVis    bool
	cmdPrompt rune
	cmdText   []rune
	dot       dotRec
	recIns    bool
	insMoved  bool // the cursor moved in Insert mode since the last edit
	replaying bool
	didChange bool
}

// New returns an empty editor in Insert mode.
func New() Editor {
	e := Editor{lines: [][]rune{{}}, vpHeight: 20}
	e.startInsertDot()
	return e
}

// startInsertDot records the Insert session the editor opens in as if it
// began with "i", so "." after the first esc repeats what was typed.
func (e *Editor) startInsertDot() {
	e.dot = dotRec{keys: []Key{{Name: "i", Text: "i"}}}
	e.recIns = true
}

// SetValue replaces the buffer, resets the cursor, history and mode (Insert).
func (e *Editor) SetValue(s string) {
	parts := strings.Split(s, "\n")
	e.lines = make([][]rune, len(parts))
	for i, p := range parts {
		e.lines[i] = []rune(p)
	}
	e.row, e.col, e.want = 0, 0, 0
	e.undo, e.redo = nil, nil
	e.snapped = false
	e.mode = Insert
	e.clearPending()
	e.startInsertDot()
	e.status = ""
	e.rev++
}

// Value returns the buffer joined with "\n". The result is cached by revision.
func (e *Editor) Value() string {
	if e.cacheOK && e.cacheRev == e.rev {
		return e.cache
	}
	n := len(e.lines)
	for _, l := range e.lines {
		n += len(l)
	}
	b := make([]byte, 0, n)
	for i, l := range e.lines {
		if i > 0 {
			b = append(b, '\n')
		}
		for _, r := range l {
			b = utf8.AppendRune(b, r)
		}
	}
	e.cache, e.cacheRev, e.cacheOK = string(b), e.rev, true
	return e.cache
}

// Revision increments on every text change, never on pure cursor moves.
func (e *Editor) Revision() uint64 { return e.rev }

// LineCount is the number of buffer lines (always at least one).
func (e *Editor) LineCount() int { return len(e.lines) }

// Line returns line i as a read-only view; callers must not modify it.
func (e *Editor) Line(i int) []rune {
	if i < 0 || i >= len(e.lines) {
		return nil
	}
	return e.lines[i]
}

// Cursor is the current cursor position.
func (e *Editor) Cursor() Pos { return Pos{e.row, e.col} }

// Mode is the current mode.
func (e *Editor) Mode() Mode { return e.mode }

// SetViewportHeight sets the page size used by ctrl+d/u/f/b and H/M/L.
func (e *Editor) SetViewportHeight(h int) {
	if h < 1 {
		h = 1
	}
	e.vpHeight = h
}

// SetTop tells the engine the first visible row (for H/M/L).
func (e *Editor) SetTop(top int) { e.top = max(top, 0) }

// CommandLine is the prompt char plus typed text in Command mode, else "".
func (e *Editor) CommandLine() string {
	if e.mode != Command {
		return ""
	}
	return string(e.cmdPrompt) + string(e.cmdText)
}

// Pending is the not-yet-complete count/operator/prefix keys, for a showcmd area.
func (e *Editor) Pending() string { return e.pend }

// Status is the last transient message; cleared at the start of the next HandleKey.
func (e *Editor) Status() string { return e.status }

// Selection returns the Visual selection; ok only in Visual and VisualLine.
func (e *Editor) Selection() (Selection, bool) {
	if e.mode != Visual && e.mode != VisualLine {
		return Selection{}, false
	}
	a, b := e.clampPos(e.vstart), Pos{e.row, e.col}
	if posLess(b, a) {
		a, b = b, a
	}
	return Selection{Start: a, End: b, Linewise: e.mode == VisualLine}, true
}

// HandleKey processes one key press and reports what the host should do.
func (e *Editor) HandleKey(k Key) Action {
	e.status = ""
	if k.Name == "" && e.mode != Insert {
		k.Name = k.Text
	}
	row, col, rev := e.row, e.col, e.rev
	e.keepWant = false
	act := e.handle(k)
	if e.mode != Insert {
		e.snapped = false
	}
	e.clamp()
	if !e.keepWant && (e.row != row || e.col != col || e.rev != rev) {
		e.want = e.col
	}
	return act
}

func (e *Editor) handle(k Key) Action {
	switch e.mode {
	case Insert:
		return e.insertKey(k)
	case Command:
		return e.commandKey(k)
	default:
		return e.normalKey(k)
	}
}

// InsertText inserts pasted text at the cursor as one undo step.
func (e *Editor) InsertText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return
	}
	switch e.mode {
	case Command:
		e.cmdText = append(e.cmdText, []rune(strings.ReplaceAll(s, "\n", ""))...)
	case Insert:
		e.clamp()
		e.insertKey(Key{Name: pasteKey, Text: s})
	default:
		e.clamp()
		at := Pos{e.row, e.col}
		if len(e.lines[e.row]) > 0 {
			at.Col++
		}
		end := e.insertAt(at, s)
		e.row, e.col = end.Row, max(end.Col-1, 0)
		e.want = e.col
		e.snapped = false
		// "." repeats the paste, as an append of the same text.
		e.dot = dotRec{keys: []Key{{Name: "a", Text: "a"}}, ins: []Key{{Name: pasteKey, Text: s}}}
	}
	e.clamp()
}

// ---- cursor helpers ----

func (e *Editor) lastRow() int { return len(e.lines) - 1 }

func (e *Editor) clamp() {
	e.row = max(0, min(e.row, e.lastRow()))
	hi := len(e.lines[e.row]) - 1
	if e.mode == Insert {
		hi++
	}
	e.col = max(0, min(e.col, max(hi, 0)))
}

func (e *Editor) clampPos(p Pos) Pos {
	p.Row = max(0, min(p.Row, e.lastRow()))
	p.Col = max(0, min(p.Col, len(e.lines[p.Row])))
	return p
}

func posLess(a, b Pos) bool {
	return a.Row < b.Row || (a.Row == b.Row && a.Col < b.Col)
}

func isBlank(r rune) bool { return r == ' ' || r == '\t' }

// indentLen is the number of leading blanks of row r.
func (e *Editor) indentLen(r int) int {
	l := e.lines[r]
	i := 0
	for i < len(l) && isBlank(l[i]) {
		i++
	}
	return i
}

// firstNonBlank is the column of the first non-blank rune of row r (the last
// column on an all-blank line, 0 on an empty one).
func (e *Editor) firstNonBlank(r int) int {
	i := e.indentLen(r)
	if i >= len(e.lines[r]) {
		return max(len(e.lines[r])-1, 0)
	}
	return i
}

func (e *Editor) cursorFNB(r int) {
	e.row = max(0, min(r, e.lastRow()))
	e.col = e.firstNonBlank(e.row)
}

// colFor is the column the cursor takes on row r for a vertical motion.
func (e *Editor) colFor(r int) int {
	hi := max(len(e.lines[r])-1, 0)
	if e.want < 0 {
		return hi
	}
	return min(e.want, hi)
}

// ---- undo ----

func (e *Editor) pushUndo(s snapshot) {
	e.undo = append(e.undo, s)
	if len(e.undo) > maxUndo {
		e.undo = slices.Delete(e.undo, 0, 1)
	}
}

// touch takes the undo snapshot for the current change group, once.
func (e *Editor) touch() {
	if e.snapped {
		return
	}
	e.snapped = true
	e.redo = nil
	e.pushUndo(snapshot{slices.Clone(e.lines), e.row, e.col})
}

func (e *Editor) undoSteps(n int) {
	for i := 0; i < n; i++ {
		if len(e.undo) == 0 {
			e.status = "Already at oldest change"
			break
		}
		s := e.undo[len(e.undo)-1]
		e.undo = e.undo[:len(e.undo)-1]
		e.redo = append(e.redo, snapshot{e.lines, e.row, e.col})
		e.lines, e.row, e.col = s.lines, s.row, s.col
		e.rev++
	}
	e.snapped = false
}

func (e *Editor) redoSteps(n int) {
	for i := 0; i < n; i++ {
		if len(e.redo) == 0 {
			e.status = "Already at newest change"
			break
		}
		s := e.redo[len(e.redo)-1]
		e.redo = e.redo[:len(e.redo)-1]
		e.pushUndo(snapshot{e.lines, e.row, e.col})
		e.lines, e.row, e.col = s.lines, s.row, s.col
		e.rev++
	}
	e.snapped = false
}

// ---- buffer mutation primitives (every text change goes through these) ----

func (e *Editor) setLine(r int, l []rune) {
	e.touch()
	e.lines[r] = l
	e.rev++
}

// splice replaces rows r1..r2 (inclusive) with repl.
func (e *Editor) splice(r1, r2 int, repl [][]rune) {
	e.touch()
	e.lines = slices.Replace(e.lines, r1, r2+1, repl...)
	if len(e.lines) == 0 {
		e.lines = [][]rune{{}}
	}
	e.rev++
}

func (e *Editor) insertRows(at int, rows [][]rune) {
	e.touch()
	e.lines = slices.Insert(e.lines, at, rows...)
	e.rev++
}

// deleteRange removes the text in [a, b) (b exclusive, may be on a later row).
func (e *Editor) deleteRange(a, b Pos) {
	nl := slices.Concat(e.lines[a.Row][:a.Col], e.lines[b.Row][b.Col:])
	if a.Row == b.Row {
		e.setLine(a.Row, nl)
		return
	}
	e.splice(a.Row, b.Row, [][]rune{nl})
}

// textRange returns the text in [a, b), rows joined with "\n".
func (e *Editor) textRange(a, b Pos) string {
	if a.Row == b.Row {
		return string(e.lines[a.Row][a.Col:b.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(e.lines[a.Row][a.Col:]))
	for r := a.Row + 1; r < b.Row; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.lines[r]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(e.lines[b.Row][:b.Col]))
	return sb.String()
}

// insertAt inserts s (which may contain "\n") at p and returns the position
// just after the inserted text.
func (e *Editor) insertAt(p Pos, s string) Pos {
	parts := strings.Split(s, "\n")
	line := e.lines[p.Row]
	if len(parts) == 1 {
		add := []rune(parts[0])
		e.setLine(p.Row, slices.Concat(line[:p.Col], add, line[p.Col:]))
		return Pos{p.Row, p.Col + len(add)}
	}
	repl := make([][]rune, len(parts))
	for i, part := range parts {
		repl[i] = []rune(part)
	}
	repl[0] = slices.Concat(line[:p.Col], repl[0])
	end := Pos{p.Row + len(parts) - 1, len(repl[len(parts)-1])}
	repl[len(parts)-1] = slices.Concat(repl[len(parts)-1], line[p.Col:])
	e.splice(p.Row, p.Row, repl)
	return end
}

func (e *Editor) rowsText(r1, r2 int) string {
	var sb strings.Builder
	for r := r1; r <= r2; r++ {
		if r > r1 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(e.lines[r]))
	}
	return sb.String()
}

func (e *Editor) recordIns(k Key) {
	if e.insMoved && !e.replaying {
		// The first edit after an Insert-mode cursor move starts a new
		// repeatable insert there, as in vim.
		e.dot = dotRec{keys: []Key{{Name: "i", Text: "i"}}}
		e.recIns, e.insMoved = true, false
	}
	if e.recIns && !e.replaying {
		e.dot.ins = append(e.dot.ins, k)
	}
}
