package vimedit

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (e *Editor) enterCommand(prompt rune, prefill string) {
	e.mode = Command
	e.cmdPrompt = prompt
	e.cmdText = []rune(prefill)
}

func (e *Editor) commandKey(k Key) Action {
	opSearch := e.op != "" && e.cmdPrompt != ':'
	if opSearch {
		e.cmdKeys = append(e.cmdKeys, k) // so "." replays the pattern too
	}
	switch k.Name {
	case "esc":
		e.mode = Normal
		e.cancel()
	case "backspace":
		if len(e.cmdText) == 0 {
			e.mode = Normal
			e.cancel()
			return None
		}
		e.cmdText = e.cmdText[:len(e.cmdText)-1]
	case "ctrl+u":
		e.cmdText = e.cmdText[:0]
	case "enter":
		text := string(e.cmdText)
		prompt := e.cmdPrompt
		e.mode = Normal
		if prompt == ':' {
			return e.execEx(text)
		}
		// vim: an unescaped delimiter ends the pattern ("//" reuses the last
		// one); a search offset after it is not supported and is dropped.
		text = splitDelim(text, prompt, 2)[0]
		if opSearch {
			e.setSearch(text, prompt == '?')
			e.runMotion("n", 0)
			return None
		}
		e.runSearchCommand(text, prompt == '?')
	default:
		if k.Text != "" {
			e.cmdText = append(e.cmdText, []rune(k.Text)...)
		}
	}
	return None
}

// ---- ex commands ----

func (e *Editor) parseAddr(s string) (row int, rest string, ok bool) {
	s = strings.TrimLeft(s, " ")
	switch {
	case strings.HasPrefix(s, "."):
		return e.row, s[1:], true
	case strings.HasPrefix(s, "$"):
		return e.lastRow(), s[1:], true
	case strings.HasPrefix(s, "'<"), strings.HasPrefix(s, "'>"):
		if !e.hasVis {
			return 0, s, false
		}
		i := 0
		if s[1] == '>' {
			i = 1
		}
		return e.visRows[i], s[2:], true
	case s != "" && s[0] >= '0' && s[0] <= '9':
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		n, _ := strconv.Atoi(s[:i])
		return n - 1, s[i:], true
	}
	return 0, s, false
}

func (e *Editor) execEx(text string) Action {
	cmd := strings.TrimLeft(text, ": ")
	r1, r2, rest, hasRange := e.parseRange(cmd)
	rest = strings.TrimSpace(rest)
	if hasRange && rest == "" {
		e.cursorFNB(max(0, min(r2, e.lastRow())))
		return None
	}
	name := rest
	for i, c := range rest {
		if !unicode.IsLetter(c) {
			name = rest[:i]
			break
		}
	}
	args := rest[len(name):]
	switch name {
	case "":
		if rest == "" {
			return None
		}
	case "w", "wq", "x":
		return Apply
	case "q":
		if strings.HasPrefix(args, "!") {
			return ForceClose
		}
		return Close
	case "noh", "nohlsearch":
		return None
	case "s":
		e.substitute(args, r1, r2)
		return None
	}
	e.status = "Not an editor command: " + cmd
	return None
}

func (e *Editor) parseRange(s string) (r1, r2 int, rest string, has bool) {
	r1, r2 = e.row, e.row
	if t := strings.TrimLeft(s, " "); strings.HasPrefix(t, "%") {
		return 0, e.lastRow(), t[1:], true
	}
	a, rest, ok := e.parseAddr(s)
	if !ok {
		return r1, r2, s, false
	}
	r1, r2 = a, a
	if t := strings.TrimLeft(rest, " "); strings.HasPrefix(t, ",") {
		if b, rest2, ok2 := e.parseAddr(t[1:]); ok2 {
			r2, rest = b, rest2
		} else {
			rest = t[1:]
		}
	}
	r1 = max(0, min(r1, e.lastRow()))
	r2 = max(0, min(r2, e.lastRow()))
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	return r1, r2, rest, true
}

// compilePattern compiles an RE2 pattern, falling back to a literal match.
func compilePattern(pat string, fold bool) *regexp.Regexp {
	prefix := ""
	if fold {
		prefix = "(?i)"
	}
	re, err := regexp.Compile(prefix + pat)
	if err != nil {
		re = regexp.MustCompile(prefix + regexp.QuoteMeta(pat))
	}
	return re
}

// splitDelim splits s at unescaped delim, turning "\<delim>" into delim and
// keeping every other escape for the caller.
func splitDelim(s string, delim rune, n int) []string {
	var parts []string
	var cur strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			if rs[i+1] == delim {
				cur.WriteRune(delim)
			} else {
				cur.WriteRune(c)
				cur.WriteRune(rs[i+1])
			}
			i++
		case c == delim && len(parts) < n-1:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	return append(parts, cur.String())
}

func expandRepl(rep string, line string, m []int) string {
	var sb strings.Builder
	rs := []rune(rep)
	group := func(n int) string {
		if 2*n+1 < len(m) && m[2*n] >= 0 {
			return line[m[2*n]:m[2*n+1]]
		}
		return ""
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '&':
			sb.WriteString(group(0))
		case c == '\\' && i+1 < len(rs):
			i++
			if d := rs[i]; d >= '0' && d <= '9' {
				sb.WriteString(group(int(d - '0')))
			} else {
				sb.WriteRune(d)
			}
		default:
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func (e *Editor) substitute(args string, r1, r2 int) {
	rs := []rune(args)
	if len(rs) == 0 || unicode.IsLetter(rs[0]) || unicode.IsDigit(rs[0]) || rs[0] == ' ' {
		e.status = "Not an editor command: s" + args
		return
	}
	parts := splitDelim(string(rs[1:]), rs[0], 3)
	pat, rep, flags := parts[0], "", ""
	if len(parts) > 1 {
		rep = parts[1]
	}
	if len(parts) > 2 {
		flags = parts[2]
	}
	if pat == "" {
		pat = e.search
	} else {
		e.searchWord = false
	}
	if pat == "" {
		e.status = "No previous regular expression"
		return
	}
	// vim: the pattern becomes the last search, for n and a later :s//.
	e.search = pat
	re := compilePattern(pat, strings.Contains(flags, "i"))
	n := 1
	if strings.Contains(flags, "g") {
		n = -1
	}
	total, lines, lastRow := 0, 0, -1
	for r := r1; r <= r2; r++ {
		s := string(e.lines[r])
		lim := n
		if e.searchWord {
			lim = -1 // the first whole-word match may follow partial ones
		}
		ms := e.wordMatches(s, re.FindAllStringSubmatchIndex(s, lim))
		if n > 0 && len(ms) > n {
			ms = ms[:n]
		}
		if len(ms) == 0 {
			continue
		}
		var sb strings.Builder
		prev := 0
		for _, m := range ms {
			sb.WriteString(s[prev:m[0]])
			sb.WriteString(expandRepl(rep, s, m))
			prev = m[1]
		}
		sb.WriteString(s[prev:])
		total += len(ms)
		lines++
		lastRow = r
		e.setLine(r, []rune(sb.String()))
	}
	if total == 0 {
		e.status = "Pattern not found: " + pat
		return
	}
	e.row, e.col = lastRow, 0
	e.status = plural(total, "substitution") + " on " + plural(lines, "line")
}

// ---- search ----

func (e *Editor) runSearchCommand(pat string, back bool) {
	e.setSearch(pat, back)
	e.searchStatusJump(1, false)
}

// setSearch makes pat, or the last pattern when empty, the search for n.
func (e *Editor) setSearch(pat string, back bool) {
	if pat == "" {
		pat = e.search
	} else {
		e.searchWord = false
	}
	e.search, e.searchBack = pat, back
}

// searchStatusJump moves to the next match (reversed flips the direction).
func (e *Editor) searchStatusJump(cnt int, reversed bool) {
	if m, ok := e.doSearch(cnt, reversed); ok {
		e.row, e.col = m.p.Row, m.p.Col
	}
}

func (e *Editor) searchMotion(key string, cnt int) (mres, bool) {
	switch key {
	case "*", "#":
		w, start := e.wordUnderCursor()
		if w == "" {
			return mres{}, false
		}
		// Whole words are matched by isWordRune, not RE2's ASCII-only \b, so a
		// word like "café" is found too.
		e.search, e.searchWord = regexp.QuoteMeta(w), true
		e.searchBack = key == "#"
		// From the word's start, so the word under the cursor is skipped.
		return e.doSearchFrom(Pos{e.row, start}, cnt, false)
	case "N":
		return e.doSearch(cnt, true)
	}
	return e.doSearch(cnt, false)
}

// wordUnderCursor is the word at or after the cursor on its line, and the
// column it starts at.
func (e *Editor) wordUnderCursor() (string, int) {
	l := e.lines[e.row]
	c := e.col
	for c < len(l) && !isWordRune(l[c]) {
		c++
	}
	if c >= len(l) {
		return "", 0
	}
	s, t := c, c
	for s > 0 && isWordRune(l[s-1]) {
		s--
	}
	for t+1 < len(l) && isWordRune(l[t+1]) {
		t++
	}
	return string(l[s : t+1]), s
}

// wordMatches drops the matches of a whole-word search (searchWord) that
// have a word rune right before or after them.
func (e *Editor) wordMatches(s string, ms [][]int) [][]int {
	if !e.searchWord {
		return ms
	}
	kept := ms[:0]
	for _, m := range ms {
		before, _ := utf8.DecodeLastRuneInString(s[:m[0]])
		after, _ := utf8.DecodeRuneInString(s[m[1]:])
		if (m[0] == 0 || !isWordRune(before)) && (m[1] == len(s) || !isWordRune(after)) {
			kept = append(kept, m)
		}
	}
	return kept
}

// matchCols lists the rune columns where re matches in row r.
func (e *Editor) matchCols(re *regexp.Regexp, r int) []int {
	s := string(e.lines[r])
	idx := e.wordMatches(s, re.FindAllStringIndex(s, -1))
	if len(idx) == 0 {
		return nil
	}
	cols := make([]int, len(idx))
	for i, m := range idx {
		cols[i] = utf8.RuneCountInString(s[:m[0]])
	}
	return cols
}

func (e *Editor) doSearch(cnt int, reversed bool) (mres, bool) {
	return e.doSearchFrom(e.cursorPos(), cnt, reversed)
}

// doSearchFrom finds the cnt-th match of the last search from pos.
func (e *Editor) doSearchFrom(pos Pos, cnt int, reversed bool) (mres, bool) {
	if e.search == "" {
		e.status = "No previous search pattern"
		return mres{}, false
	}
	re := compilePattern(e.search, false)
	back := e.searchBack != reversed
	wrapped := false
	for i := 0; i < cnt; i++ {
		p, w, ok := e.findMatch(re, pos, back)
		if !ok {
			e.status = "Pattern not found: " + e.search
			return mres{}, false
		}
		pos = p
		wrapped = wrapped || w
	}
	if wrapped {
		if back {
			e.status = "search hit TOP, continuing at BOTTOM"
		} else {
			e.status = "search hit BOTTOM, continuing at TOP"
		}
	}
	return mres{p: pos}, true
}

func (e *Editor) findMatch(re *regexp.Regexp, from Pos, back bool) (Pos, bool, bool) {
	n := len(e.lines)
	for i := 0; i <= n; i++ {
		var r int
		if back {
			r = ((from.Row-i)%n + n) % n
		} else {
			r = (from.Row + i) % n
		}
		cols := e.matchCols(re, r)
		if len(cols) == 0 {
			continue
		}
		wrapped := (!back && from.Row+i >= n) || (back && from.Row-i < 0)
		switch {
		case i == 0 && !back:
			// A match past the last rune (such as /$) is drawn on the last
			// rune; one at the cursor's own column isn't after it.
			last := max(len(e.lines[r])-1, 0)
			if j := slices.IndexFunc(cols, func(c int) bool { return min(c, last) > from.Col }); j >= 0 {
				return Pos{r, cols[j]}, false, true
			}
		case i == 0:
			if j := lastIndexFunc(cols, func(c int) bool { return c < from.Col }); j >= 0 {
				return Pos{r, cols[j]}, false, true
			}
		case i == n:
			// Wrapped fully around to the start row: any match qualifies.
			c := cols[0]
			if back {
				c = cols[len(cols)-1]
			}
			return Pos{r, c}, true, true
		case back:
			return Pos{r, cols[len(cols)-1]}, wrapped, true
		default:
			return Pos{r, cols[0]}, wrapped, true
		}
	}
	return Pos{}, false, false
}

func lastIndexFunc(s []int, f func(int) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// plural formats n with noun, adding an "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
