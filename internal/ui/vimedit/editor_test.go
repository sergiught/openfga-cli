package vimedit

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestValueRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "a\nb", "a\nb\n", "\n", "\n\n", "héllo\nwörld"} {
		e := New()
		e.SetValue(s)
		if got := e.Value(); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
	e := New()
	e.SetValue("a\n")
	if e.LineCount() != 2 || len(e.Line(1)) != 0 {
		t.Fatalf("trailing newline must give a final empty line, got %d lines", e.LineCount())
	}
}

func TestNewIsInsertAndSetValue(t *testing.T) {
	e := New()
	if e.Mode() != Insert {
		t.Fatalf("mode = %v", e.Mode())
	}
	feed(&e, "hi<esc>")
	r := e.Revision()
	e.SetValue("x\ny")
	if e.Revision() == r || e.Mode() != Insert || e.Cursor() != (Pos{}) {
		t.Fatalf("SetValue state wrong: rev=%d mode=%v cur=%v", e.Revision(), e.Mode(), e.Cursor())
	}
	feed(&e, "<esc>u")
	if e.Value() != "x\ny" {
		t.Fatalf("undo history must be cleared, got %q", e.Value())
	}
}

func TestRevisionAndValueCache(t *testing.T) {
	e := ed("abc\ndef")
	r := e.Revision()
	feed(e, "jlw$0")
	if e.Revision() != r {
		t.Fatal("cursor moves must not bump the revision")
	}
	feed(e, "x")
	if e.Revision() == r {
		t.Fatal("edit must bump the revision")
	}
	v1 := e.Value()
	if v2 := e.Value(); v1 != v2 {
		t.Fatal("cache mismatch")
	}
}

func TestModeString(t *testing.T) {
	want := map[Mode]string{Insert: "INSERT", Normal: "NORMAL", Visual: "VISUAL", VisualLine: "V-LINE", Command: "COMMAND"}
	for m, s := range want {
		if m.String() != s {
			t.Errorf("%d -> %q", m, m.String())
		}
	}
}

func TestMotions(t *testing.T) {
	e := ed("foo bar.baz  qux\n\n  indented line\nlast")
	steps := []struct {
		keys     string
		row, col int
	}{
		{"w", 0, 4}, {"w", 0, 7}, {"w", 0, 8}, {"w", 0, 13}, {"w", 1, 0}, {"w", 2, 2},
		{"b", 1, 0}, {"b", 0, 13}, {"e", 0, 15}, {"0", 0, 0}, {"$", 0, 15}, {"^", 0, 0},
		{"W", 0, 4}, {"E", 0, 10}, {"B", 0, 4}, {"gg", 0, 0}, {"G", 3, 0}, {"2G", 1, 0},
		{"3gg", 2, 2}, {"fl", 2, 11}, {"Fn", 2, 6}, {"tn", 2, 12}, {"0", 2, 0},
	}
	for _, s := range steps {
		feed(e, s.keys)
		cursorIs(t, e, s.row, s.col)
	}
}

func TestMotionCounts(t *testing.T) {
	e := ed("a b c d e f g")
	feed(e, "3w")
	cursorIs(t, e, 0, 6)
	feed(e, "2b")
	cursorIs(t, e, 0, 2)
	feed(e, "10l")
	cursorIs(t, e, 0, 12)
	feed(e, "0j")
	cursorIs(t, e, 0, 0)
}

func TestVerticalKeepsColumn(t *testing.T) {
	e := ed("abcdefgh\nab\nabcdefgh")
	feed(e, "5l")
	feed(e, "j")
	cursorIs(t, e, 1, 1)
	feed(e, "j")
	cursorIs(t, e, 2, 5)
	feed(e, "$k")
	cursorIs(t, e, 1, 1)
	feed(e, "k")
	cursorIs(t, e, 0, 7)
}

func TestFindRepeat(t *testing.T) {
	e := ed("a,b,c,d")
	feed(e, "f,")
	cursorIs(t, e, 0, 1)
	feed(e, ";")
	cursorIs(t, e, 0, 3)
	feed(e, ",")
	cursorIs(t, e, 0, 1)
	feed(e, "0t,")
	cursorIs(t, e, 0, 0)
	feed(e, ";")
	cursorIs(t, e, 0, 2)
	feed(e, "2f,")
	cursorIs(t, e, 0, 5)
}

func TestPercentAndParagraph(t *testing.T) {
	e := ed("f(a[1], {b})\nx")
	feed(e, "f(%")
	cursorIs(t, e, 0, 11)
	feed(e, "%")
	cursorIs(t, e, 0, 1)
	e = ed("a\nb\n\nc\nd\n\n\ne")
	feed(e, "}")
	cursorIs(t, e, 2, 0)
	feed(e, "}")
	cursorIs(t, e, 5, 0)
	feed(e, "{")
	cursorIs(t, e, 2, 0)
	feed(e, "{")
	cursorIs(t, e, 0, 0)
}

func TestHML(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("  line\n")
	}
	e := ed(strings.TrimSuffix(sb.String(), "\n"))
	e.SetViewportHeight(10)
	e.SetTop(20)
	feed(e, "H")
	cursorIs(t, e, 20, 2)
	feed(e, "L")
	cursorIs(t, e, 29, 2)
	feed(e, "M")
	cursorIs(t, e, 24, 2)
	feed(e, "3H")
	cursorIs(t, e, 22, 2)
	feed(e, "2L")
	cursorIs(t, e, 28, 2)
}

func TestPaging(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("x\n")
	}
	e := ed(sb.String())
	e.SetViewportHeight(20)
	feed(e, "<c-d>")
	cursorIs(t, e, 10, 0)
	feed(e, "<c-f>")
	cursorIs(t, e, 30, 0)
	feed(e, "<c-u>")
	cursorIs(t, e, 20, 0)
	feed(e, "<c-b>")
	cursorIs(t, e, 0, 0)
	feed(e, "<pgdown><pgdown>")
	cursorIs(t, e, 40, 0)
	feed(e, "<pgup>")
	cursorIs(t, e, 20, 0)
}

func TestOperators(t *testing.T) {
	runCases(t, []tc{
		{"dw", "foo bar baz", "dw", "bar baz"},
		{"d2w", "foo bar baz", "d2w", "baz"},
		{"2dw", "foo bar baz", "2dw", "baz"},
		{"2d3w", "a b c d e f g h", "2d3w", "g h"},
		{"dw last word keeps next line", "foo bar\nnext", "wdw", "foo \nnext"},
		{"dw on only word", "foo\nbar", "dw", "\nbar"},
		{"de", "foo bar", "de", " bar"},
		{"db", "foo bar", "wdb", "bar"},
		{"d$", "foo bar", "wd$", "foo "},
		{"D", "foo bar", "wD", "foo "},
		{"d0", "foo bar", "wd0", "bar"},
		{"dd", "a\nb\nc", "dd", "b\nc"},
		{"3dd", "a\nb\nc\nd", "3dd", "d"},
		{"dd last", "a\nb", "jdd", "a"},
		{"dd only", "a", "dd", ""},
		{"dj", "a\nb\nc", "dj", "c"},
		{"dk", "a\nb\nc", "jdk", "c"},
		{"dG", "a\nb\nc", "jdG", "a"},
		{"dgg", "a\nb\nc", "jdgg", "c"},
		{"d2G", "a\nb\nc\nd", "d2G", "c\nd"},
		{"x", "abc", "x", "bc"},
		{"3x", "abcdef", "3x", "def"},
		{"X", "abc", "lX", "bc"},
		{"x end", "abc", "$x", "ab"},
		{"df", "a,b,c", "df,", "b,c"},
		{"dt", "a,b,c", "dt,", ",b,c"},
		{"dF", "a,b,c", "$dF,", "a,bc"},
		{"d%", "x(a)y", "ld%", "xy"},
		{"d}", "a\nb\n\nc", "d}", "\nc"},
		{"yyp", "a\nb", "yyp", "a\na\nb"},
		{"yyP", "a\nb", "jyyP", "a\nb\nb"},
		{"ywP", "foo bar", "ywP", "foo foo bar"},
		{"yy 3p", "a\nb", "yy3p", "a\na\na\na\nb"},
		{"p charwise", "abc", "xp", "bac"},
		{"P charwise", "abc", "xP", "abc"},
		{"3p charwise", "ab", "x3p", "baaa"},
		{"ddp", "a\nb\nc", "ddp", "b\na\nc"},
		{"yw then p after dw", "foo bar", "dwP", "foo bar"},
		{"cw", "foo bar", "cwxx<esc>", "xx bar"},
		{"cw on last char of word", "a bc", "wlcwZ<esc>", "a bZ"},
		{"c2w", "a b c d", "c2wX<esc>", "X c d"},
		{"cw on blank", "a  b", "lcwX<esc>", "aXb"},
		{"cc", "  foo\nbar", "ccx<esc>", "  x\nbar"},
		{"S", "  foo\nbar", "Sx<esc>", "  x\nbar"},
		{"C", "foo bar", "wCx<esc>", "foo x"},
		{"s", "abc", "sX<esc>", "Xbc"},
		{"3s", "abcd", "3sX<esc>", "Xd"},
		{"cj", "a\nb\nc", "cjX<esc>", "X\nc"},
		{">>", "a\nb", ">>", "  a\nb"},
		{"2>>", "a\nb\nc", "2>>", "  a\n  b\nc"},
		{"<<", "    a\n b", "<<j<<", "  a\nb"},
		{">j", "a\nb\nc", ">j", "  a\n  b\nc"},
		{">> empty line untouched", "\na", ">>", "\na"},
		{"yank then dd keeps reg", "a\nb", "yyjdd", "a"},
		{"register prefix dropped", "abc", "\"ax", "bc"},
		{"Y", "a\nb", "Yjp", "a\nb\na"},
		{"J", "a\n  b\nc", "J", "a b\nc"},
		{"3J", "a\nb\nc\nd", "3J", "a b c\nd"},
		{"J paren", "f(\n)", "J", "f()"},
		{"J trailing space", "a \nb", "J", "a b"},
		{"~", "abC", "3~", "ABc"},
		{"r", "abc", "rx", "xbc"},
		{"3r", "abcd", "3rx", "xxxd"},
		{"r too many", "ab", "3rx", "ab"},
		{"zz ignored", "ab", "zzx", "b"},
		{"esc cancels operator", "ab", "d<esc>x", "b"},
		{"unknown cancels", "ab", "dQx", "b"},
	})
}

func TestTextObjects(t *testing.T) {
	runCases(t, []tc{
		{"diw", "foo bar", "wdiw", "foo "},
		{"daw", "foo bar baz", "wdaw", "foo baz"},
		{"daw last", "foo bar", "wdaw", "foo"},
		{"diW", "a foo.bar b", "wdiW", "a  b"},
		{"daW", "a foo.bar b", "wdaW", "a b"},
		{"ciw", "foo bar", "ciwX<esc>", "X bar"},
		{"di(", "f(a, b)", "fadi(", "f()"},
		{"da(", "f(a, b) x", "fada(", "f x"},
		{"dib", "f(a)", "fadib", "f()"},
		{"di) on paren", "f(a)", "f(di)", "f()"},
		{"di[", "x[1,2]", "f1di[", "x[]"},
		{"da]", "x[1,2]y", "f1da]", "xy"},
		{"di{", "{a b}", "ldi{", "{}"},
		{"daB", "x{a}y", "fada{", "xy"},
		{"di{ nested", "{ {a} b }", "fbdi{", "{}"},
		{"di{ multiline", "t {\n  a\n  b\n}", "jdi{", "t {\n}"},
		{"ci{ multiline", "t {\n  a\n}", "jci{X<esc>", "t {\n  X\n}"},
		{"di\"", "say \"hi there\" now", "fhdi\"", "say \"\" now"},
		{"da\"", "say \"hi\" now", "fhda\"", "say now"},
		{"di'", "a 'x' b", "fxdi'", "a '' b"},
		{"di\" before quotes", "a \"x\"", "di\"", "a \"\""},
		{"yiw p", "foo bar", "yiwwP", "foo foobar"},
		{"no object", "abc", "di(", "abc"},
	})
}

func TestInsertMode(t *testing.T) {
	e := New()
	feed(&e, "hello<enter>world")
	if e.Value() != "hello\nworld" {
		t.Fatalf("%q", e.Value())
	}
	feed(&e, "<bs><bs>")
	if e.Value() != "hello\nwor" {
		t.Fatalf("%q", e.Value())
	}
	feed(&e, "<home><bs>")
	if e.Value() != "hellowor" {
		t.Fatalf("join: %q", e.Value())
	}
	cursorIs(t, &e, 0, 5)
	feed(&e, "<del>")
	if e.Value() != "helloor" {
		t.Fatalf("del: %q", e.Value())
	}
	feed(&e, "<tab>")
	if e.Value() != "hello  or" {
		t.Fatalf("tab: %q", e.Value())
	}
	feed(&e, "<end><del>")
	feed(&e, "<enter>")
	feed(&e, "<up><end><del>")
	if e.Value() != "hello  or" {
		t.Fatalf("del join: %q", e.Value())
	}
}

func TestInsertDeletes(t *testing.T) {
	e := New()
	feed(&e, "foo bar.baz<c-w>")
	if e.Value() != "foo bar." {
		t.Fatalf("%q", e.Value())
	}
	feed(&e, "<c-w>")
	if e.Value() != "foo bar" {
		t.Fatalf("%q", e.Value())
	}
	feed(&e, "<c-w><c-w>")
	if e.Value() != "" {
		t.Fatalf("%q", e.Value())
	}
	e.SetValue("")
	feed(&e, "  abc<c-u>")
	if e.Value() != "  " {
		t.Fatalf("first ctrl+u keeps indent: %q", e.Value())
	}
	feed(&e, "<c-u>")
	if e.Value() != "" {
		t.Fatalf("second ctrl+u clears indent: %q", e.Value())
	}
}

func TestInsertEntryKeys(t *testing.T) {
	runCases(t, []tc{
		{"i", "bc", "iX<esc>", "Xbc"},
		{"a", "bc", "aX<esc>", "bXc"},
		{"a empty", "", "aX<esc>", "X"},
		{"A", "bc", "AX<esc>", "bcX"},
		{"I", "  bc", "IX<esc>", "  Xbc"},
		{"o", "a\nb", "oX<esc>", "a\nX\nb"},
		{"O", "a\nb", "jOX<esc>", "a\nX\nb"},
		{"o keeps indent", "  a", "oX<esc>", "  a\n  X"},
		{"O keeps indent", "  a", "OX<esc>", "  X\n  a"},
		{"esc moves left", "ab", "aX<esc>iY<esc>", "aYXb"},
	})
}

func TestAutoIndent(t *testing.T) {
	runCases(t, []tc{
		{"model", "model", "A<enter>schema 1.1<esc>", "model\n  schema 1.1"},
		{"type", "type user", "A<enter>relations<esc>", "type user\n  relations"},
		{"relations", "  relations", "A<enter>define a: [user]<esc>", "  relations\n    define a: [user]"},
		{"o after type", "type doc", "ox<esc>", "type doc\n  x"},
		{"o after relations", "  relations", "ox<esc>", "  relations\n    x"},
		{"O no extra", "type doc", "Ox<esc>", "x\ntype doc"},
		{"plain", "    define a: b", "A<enter>x<esc>", "    define a: b\n    x"},
		{"enter in middle", "ab", "a<enter>X<esc>", "a\nXb"},
		{"enter trims moved blanks", "a  b", "a<enter>X<esc>", "a\nXb"},
	})
}

func TestEnterMiddleTrim(t *testing.T) {
	e := New()
	e.SetValue("  a  b")
	feed(&e, "<esc>fa")
	feed(&e, "a<enter>X<esc>")
	if e.Value() != "  a\n  Xb" {
		t.Fatalf("%q", e.Value())
	}
}

func TestInsertSessionIsOneUndo(t *testing.T) {
	e := ed("abc")
	feed(e, "AXYZ<enter>more<esc>")
	if e.Value() != "abcXYZ\nmore" {
		t.Fatalf("%q", e.Value())
	}
	feed(e, "u")
	if e.Value() != "abc" {
		t.Fatalf("one undo should revert the whole session: %q", e.Value())
	}
	feed(e, "<c-r>")
	if e.Value() != "abcXYZ\nmore" {
		t.Fatalf("redo: %q", e.Value())
	}
}

func TestArrowsBreakUndoGroup(t *testing.T) {
	e := ed("")
	feed(e, "iab<left>cd<esc>")
	if e.Value() != "acdb" {
		t.Fatalf("%q", e.Value())
	}
	feed(e, "u")
	if e.Value() != "ab" {
		t.Fatalf("%q", e.Value())
	}
}

func TestUndoRedo(t *testing.T) {
	e := ed("a b c")
	feed(e, "dwdw")
	if e.Value() != "c" {
		t.Fatal(e.Value())
	}
	feed(e, "u")
	if e.Value() != "b c" {
		t.Fatal(e.Value())
	}
	cursorIs(t, e, 0, 0)
	feed(e, "u")
	if e.Value() != "a b c" {
		t.Fatal(e.Value())
	}
	feed(e, "u")
	if e.Status() != "Already at oldest change" {
		t.Fatalf("status %q", e.Status())
	}
	feed(e, "<c-r><c-r>")
	if e.Value() != "c" {
		t.Fatal(e.Value())
	}
	feed(e, "x")
	feed(e, "<c-r>")
	if e.Value() != "" {
		t.Fatal(e.Value())
	}
	feed(e, "2u")
	if e.Value() != "b c" {
		t.Fatalf("2u: %q", e.Value())
	}
}

func TestUndoCap(t *testing.T) {
	e := ed(strings.Repeat("x", 700))
	for i := 0; i < 700; i++ {
		feed(e, "x")
	}
	n := 0
	for e.Value() != strings.Repeat("x", 700) && n < 1000 {
		feed(e, "u")
		n++
	}
	if len(e.undo) > maxUndo {
		t.Fatalf("undo stack %d exceeds cap", len(e.undo))
	}
}

func TestDotRepeat(t *testing.T) {
	runCases(t, []tc{
		{"x", "abcde", "x.", "cde"},
		{"dw", "a b c d", "dw.", "c d"},
		{"dw count", "a b c d e", "dw2.", "d e"},
		{"3dw then .", "a b c d e f", "2dw.", "e f"},
		{"dd", "a\nb\nc", "dd.", "c"},
		{"insert text", "x", "iab<esc>j.", "aabbx"},
		{"A", "a\nb", "A!<esc>j.", "a!\nb!"},
		{"o", "a", "ox<esc>.", "a\nx\nx"},
		{"cw", "foo bar baz", "cwX<esc>w.", "X X baz"},
		{"cw second", "foo bar baz", "cwX<esc>w.w.", "X X X"},
		{"r", "abcd", "rxl.", "xxcd"},
		{"p", "a", "yyp.", "a\na\na"},
		{"J", "a\nb\nc", "J.", "a b c"},
		{">>", "a\nb", ">>j.", "  a\n  b"},
		{"~", "abcd", "~.", "ABcd"},
		{"cc", "a\nb", "ccX<esc>j.", "X\nX"},
	})
}

func TestDotUndoIsOneStep(t *testing.T) {
	e := ed("a b c d")
	feed(e, "cwX<esc>w.")
	if e.Value() != "X X c d" {
		t.Fatal(e.Value())
	}
	feed(e, "u")
	if e.Value() != "X b c d" {
		t.Fatalf("%q", e.Value())
	}
}

func TestVisual(t *testing.T) {
	runCases(t, []tc{
		{"vd", "abcdef", "lvld", "adef"},
		{"vwd", "foo bar baz", "vwd", "ar baz"},
		{"vx", "abc", "vx", "bc"},
		{"vc", "abc", "vlcX<esc>", "Xc"},
		{"vs", "abc", "vsX<esc>", "Xbc"},
		{"vy p", "abc", "vly$p", "abcab"},
		{"vy cursor to start", "abc", "$vhyx", "ac"},
		{"Vd", "a\nb\nc", "Vjd", "c"},
		{"Vy p", "a\nb", "VyGp", "a\nb\na"},
		{"V>", "a\nb\nc", "Vj>", "  a\n  b\nc"},
		{"V2>", "a\nb", "V2>", "    a\nb"},
		{"v<", "    a\n    b", "vj<", "  a\n  b"},
		{"vJ", "a\nb\nc", "VjJ", "a b\nc"},
		{"vJ single", "a\nb\nc", "VJ", "a b\nc"},
		{"v~", "abc", "vl~", "ABc"},
		{"vU", "abc", "vlU", "ABc"},
		{"vu", "ABC", "vlu", "abC"},
		{"v p", "foo bar", "yiwwviwp", "foo foo"},
		{"V p", "a\nb\nc", "yyjVp", "a\na\nc"},
		{"vp charwise with linewise reg", "a\nbcd", "yyjlvp", "a\nb\na\nd"},
		{"vp swaps reg", "foo bar", "yiwwviwpP", "foo fobaro"},
		{"vr", "abcd", "vllrx", "xxxd"},
		{"Vr", "ab\ncd", "Vrx", "xx\ncd"},
		{"viw", "foo bar", "wviwd", "foo "},
		{"vi(", "f(ab)", "fbvi(d", "f()"},
		{"va\"", "a \"b\" c", "fbva\"d", "a c"},
		{"o swaps", "abcdef", "llvlohd", "aef"},
		{"v toggles off", "abc", "vvx", "bc"},
		{"v to V", "a\nb", "vVd", "b"},
		{"V to v", "abc", "Vvld", "c"},
		{"esc exits", "abc", "v<esc>x", "bc"},
		{"v$ d", "abc\ndef", "lv$d", "a\ndef"},
		{"vgg", "a\nb\nc", "GVggd", ""},
		{"count motion", "a b c d", "v2wd", " d"},
	})
}

func TestVisualSelection(t *testing.T) {
	e := ed("abc\ndef")
	if _, ok := e.Selection(); ok {
		t.Fatal("no selection in Normal")
	}
	feed(e, "lvjl")
	sel, ok := e.Selection()
	if !ok || sel.Start != (Pos{0, 1}) || sel.End != (Pos{1, 2}) || sel.Linewise {
		t.Fatalf("%+v %v", sel, ok)
	}
	feed(e, "o")
	sel, _ = e.Selection()
	if sel.Start != (Pos{0, 1}) || sel.End != (Pos{1, 2}) {
		t.Fatalf("ordered: %+v", sel)
	}
	feed(e, "V")
	sel, _ = e.Selection()
	if !sel.Linewise || e.Mode() != VisualLine {
		t.Fatalf("%+v", sel)
	}
}

func TestSubstitute(t *testing.T) {
	runCases(t, []tc{
		{"current line", "aa\naa", ":s/a/b/<enter>", "ba\naa"},
		{"g flag", "aa\naa", ":s/a/b/g<enter>", "bb\naa"},
		{"percent", "aa\naa", ":%s/a/b/g<enter>", "bb\nbb"},
		{"range", "a\na\na\na", ":2,3s/a/b/<enter>", "a\nb\nb\na"},
		{"dot and dollar", "a\na\na", ":.,$s/a/b/<enter>", "b\nb\nb"},
		{"groups", "john smith", ":s/(\\w+) (\\w+)/\\2 \\1/<enter>", "smith john"},
		{"ampersand", "abc", ":s/b/[&]/<enter>", "a[b]c"},
		{"escaped ampersand", "abc", ":s/b/\\&/<enter>", "a&c"},
		{"escaped slash", "a/b", ":s/\\//-/<enter>", "a-b"},
		{"other delimiter", "a/b", ":s#/#-#<enter>", "a-b"},
		{"i flag", "aA", ":s/a/x/gi<enter>", "xx"},
		{"literal fallback", "a(b", ":s/(/X/<enter>", "aXb"},
		{"no trailing delim", "abc", ":s/b/X<enter>", "aXc"},
		{"empty replacement", "abc", ":s/b//<enter>", "ac"},
		{"not found", "abc", ":s/z/y/<enter>", "abc"},
		{"numbers", "a\nb\nc", ":2s/./X/<enter>", "a\nX\nc"},
		{"visual range", "a\na\na\na", "jVj:s/a/b/<enter>", "a\nb\nb\na"},
		{"visual range via marks", "a\na\na", "Vj<esc>:'<,'>s/a/b/<enter>", "b\nb\na"},
	})
}

func TestSubstituteStatusAndUndo(t *testing.T) {
	e := ed("aa\nba\nc")
	feed(e, ":%s/a/x/g<enter>")
	if e.Status() != "3 substitutions on 2 lines" {
		t.Fatalf("%q", e.Status())
	}
	if e.Value() != "xx\nbx\nc" {
		t.Fatal(e.Value())
	}
	feed(e, "u")
	if e.Value() != "aa\nba\nc" {
		t.Fatalf("one undo step: %q", e.Value())
	}
	feed(e, ":s/zzz/y/<enter>")
	if e.Status() != "Pattern not found: zzz" {
		t.Fatalf("%q", e.Status())
	}
}

func TestExCommands(t *testing.T) {
	cases := []struct {
		keys string
		want Action
	}{
		{":w<enter>", Apply}, {":wq<enter>", Apply}, {":x<enter>", Apply},
		{":q<enter>", Close}, {":q!<enter>", ForceClose}, {":noh<enter>", None},
	}
	for _, c := range cases {
		e := ed("abc")
		if got := feed(e, c.keys); got != c.want {
			t.Errorf("%s: %v want %v", c.keys, got, c.want)
		}
		if e.Mode() != Normal {
			t.Errorf("%s: mode %v", c.keys, e.Mode())
		}
	}
	e := ed("a\nb\nc")
	feed(e, ":2<enter>")
	cursorIs(t, e, 1, 0)
	feed(e, ":$<enter>")
	cursorIs(t, e, 2, 0)
	feed(e, ":99<enter>")
	cursorIs(t, e, 2, 0)
	feed(e, ":1<enter>")
	cursorIs(t, e, 0, 0)
	feed(e, ":zz<enter>")
	if e.Status() != "Not an editor command: zz" {
		t.Fatalf("%q", e.Status())
	}
}

func TestCommandLineEditing(t *testing.T) {
	e := ed("abc")
	feed(e, ":wq")
	if e.Mode() != Command || e.CommandLine() != ":wq" {
		t.Fatalf("%v %q", e.Mode(), e.CommandLine())
	}
	feed(e, "<bs>")
	if e.CommandLine() != ":w" {
		t.Fatal(e.CommandLine())
	}
	feed(e, "<c-u>")
	if e.CommandLine() != ":" {
		t.Fatal(e.CommandLine())
	}
	feed(e, "<bs>")
	if e.Mode() != Normal || e.CommandLine() != "" {
		t.Fatalf("%v %q", e.Mode(), e.CommandLine())
	}
	feed(e, "/foo<esc>")
	if e.Mode() != Normal {
		t.Fatal("esc cancels")
	}
	feed(e, "v:")
	if e.CommandLine() != ":'<,'>" || e.Mode() != Command {
		t.Fatalf("%q", e.CommandLine())
	}
	feed(e, "<esc>/ab")
	if e.CommandLine() != "/ab" {
		t.Fatal(e.CommandLine())
	}
}

func TestSearch(t *testing.T) {
	e := ed("foo bar\nbaz foo\nqux foo bar")
	feed(e, "/foo<enter>")
	cursorIs(t, e, 1, 4)
	feed(e, "n")
	cursorIs(t, e, 2, 4)
	feed(e, "n")
	if e.Status() != "search hit BOTTOM, continuing at TOP" {
		t.Fatalf("%q", e.Status())
	}
	cursorIs(t, e, 0, 0)
	feed(e, "N")
	if e.Status() != "search hit TOP, continuing at BOTTOM" {
		t.Fatalf("%q", e.Status())
	}
	cursorIs(t, e, 2, 4)
	feed(e, "N")
	cursorIs(t, e, 1, 4)
	feed(e, "?bar<enter>")
	cursorIs(t, e, 0, 4)
	feed(e, "n")
	if e.Status() == "" {
		t.Fatal("backward search should wrap")
	}
	cursorIs(t, e, 2, 8)
	feed(e, "/nomatch<enter>")
	if e.Status() != "Pattern not found: nomatch" {
		t.Fatalf("%q", e.Status())
	}
	cursorIs(t, e, 2, 8)
}

func TestSearchEmptyReusesAndRegexFallback(t *testing.T) {
	e := ed("a1 a2\na3")
	feed(e, "/a[0-9]<enter>")
	cursorIs(t, e, 0, 3)
	feed(e, "/<enter>")
	cursorIs(t, e, 1, 0)
	feed(e, "/(<enter>")
	if e.Status() != "Pattern not found: (" {
		t.Fatalf("%q", e.Status())
	}
}

func TestSearchSingleMatchWraps(t *testing.T) {
	e := ed("xfoo\nbar")
	feed(e, "l/foo<enter>")
	cursorIs(t, e, 0, 1)
	if e.Status() == "" {
		t.Fatal("expected wrap message when only match is under the cursor")
	}
}

func TestStar(t *testing.T) {
	e := ed("foo foobar foo\nfoo")
	feed(e, "*")
	cursorIs(t, e, 0, 11)
	feed(e, "*")
	cursorIs(t, e, 1, 0)
	feed(e, "#")
	cursorIs(t, e, 0, 11)
	feed(e, "n")
	cursorIs(t, e, 0, 0)
}

func TestPaste(t *testing.T) {
	e := New()
	e.SetValue("ab")
	feed(&e, "<right>")
	e.InsertText("X\r\nY\rZ\t")
	if e.Value() != "aX\nY\nZ\tb" {
		t.Fatalf("%q", e.Value())
	}
	if e.Mode() != Insert {
		t.Fatal("mode must be unchanged")
	}
	feed(&e, "<esc>u")
	if e.Value() != "ab" {
		t.Fatalf("paste must be one undo step: %q", e.Value())
	}

	e = *ed("abc")
	feed(&e, "l")
	e.InsertText("XY")
	if e.Value() != "abXYc" || e.Mode() != Normal {
		t.Fatalf("%q %v", e.Value(), e.Mode())
	}
	cursorIs(t, &e, 0, 3)
	feed(&e, "u")
	if e.Value() != "abc" {
		t.Fatalf("%q", e.Value())
	}

	e = *ed("")
	e.InsertText("hi")
	if e.Value() != "hi" {
		t.Fatalf("empty line: %q", e.Value())
	}

	e = *ed("abc")
	feed(&e, ":")
	e.InsertText("w\nq")
	if e.CommandLine() != ":wq" {
		t.Fatalf("%q", e.CommandLine())
	}
}

func TestPending(t *testing.T) {
	e := ed("abc def")
	for _, c := range []struct{ keys, want string }{
		{"2d", "2d"}, {"<esc>", ""}, {"g", "g"}, {"<esc>", ""}, {"\"", "\""},
		{"<esc>", ""}, {"di", "di"}, {"<esc>", ""}, {"f", "f"}, {"<esc>", ""},
	} {
		feed(e, c.keys)
		if e.Pending() != c.want {
			t.Fatalf("after %q pending %q want %q", c.keys, e.Pending(), c.want)
		}
	}
	if a := feed(e, "d<esc>"); a != None {
		t.Fatal("esc with pending must not close")
	}
	if a := feed(e, "<esc>"); a != Close {
		t.Fatal("esc with nothing pending closes")
	}
}

func TestStatusMessages(t *testing.T) {
	e := ed("a\nb\nc\nd")
	feed(e, "3dd")
	if e.Status() != "3 fewer lines" {
		t.Fatalf("%q", e.Status())
	}
	feed(e, "l")
	if e.Status() != "" {
		t.Fatal("status clears on next key")
	}
}

func TestZIsNotStuck(t *testing.T) {
	e := ed("abc")
	feed(e, "ztzbzz")
	if e.Pending() != "" {
		t.Fatalf("%q", e.Pending())
	}
}

func TestCountCap(t *testing.T) {
	e := ed("abc")
	feed(e, "99999999x")
	if e.Value() != "" {
		t.Fatalf("%q", e.Value())
	}
}

func TestSpaceAndBackspaceMoveInNormal(t *testing.T) {
	e := ed("abc")
	feed(e, "<space>")
	cursorIs(t, e, 0, 1)
	feed(e, "<bs>")
	cursorIs(t, e, 0, 0)
	feed(e, "<right><right><left>")
	cursorIs(t, e, 0, 1)
	feed(e, "<end>")
	cursorIs(t, e, 0, 2)
	feed(e, "<home>")
	cursorIs(t, e, 0, 0)
}

func TestImmutableLinesSnapshots(t *testing.T) {
	e := ed("abc\ndef")
	old := e.Line(0)
	feed(e, "x")
	if string(old) != "abc" {
		t.Fatalf("stored line was mutated in place: %q", string(old))
	}
}

func TestPerf1000Lines(t *testing.T) {
	data, err := os.ReadFile("../../fga/testdata/stress_model.fga")
	if err != nil {
		t.Skip("stress model not available")
	}
	e := New()
	e.SetValue(string(data))
	feed(&e, "<esc>")
	if e.LineCount() < 900 {
		t.Fatalf("only %d lines", e.LineCount())
	}
	start := time.Now()
	n := 0
	for n < 2000 {
		for _, seq := range []string{"20j", "A hello<esc>", "k", "dd", "p", "u", "5k", "ix<enter>y<esc>", "u", "w", "/define<enter>"} {
			feed(&e, seq)
			_ = e.Value()
			n += len(seq)
			if n >= 2000 {
				break
			}
		}
	}
	// ~50ms normally and ~1s under -race with every package testing in
	// parallel; the bubbles textarea this replaced took ~230ms per keystroke.
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("2000 keystrokes took %v", d)
	}
}

func TestRandomKeysKeepInvariants(t *testing.T) {
	keys := []string{
		"h", "j", "k", "l", "w", "b", "e", "W", "B", "E", "0", "^", "$", "G", "gg", "d", "c", "y", ">", "<",
		"x", "X", "D", "C", "s", "S", "p", "P", "J", "~", "u", "<c-r>", ".", "i", "a", "I", "A", "o", "O",
		"v", "V", "<esc>", "<enter>", "<bs>", "<del>", "<tab>", "f", "t", ";", ",", "%", "{", "}", "n", "N",
		"*", "#", "iw", "aw", "i(", "a{", "i\"", "r", "z", "1", "2", "0", "foo", " ", "(", ")", "\"", ":s/o/0/g<enter>",
		"/o<enter>", "<c-d>", "<c-u>", "<up>", "<down>", "<home>", "<end>", "<c-w>",
	}
	seed := uint64(12345)
	next := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(max(n, 1))) //nolint:gosec // test PRNG, n is a small positive length
	}
	e := New()
	e.SetValue("model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define a: [user] or b\n\n    define b: [user]")
	for i := 0; i < 20000; i++ {
		feed(&e, keys[next(len(keys))])
		c := e.Cursor()
		if e.LineCount() < 1 || c.Row < 0 || c.Row >= e.LineCount() || c.Col < 0 {
			t.Fatalf("step %d: bad cursor %+v lines=%d", i, c, e.LineCount())
		}
		n := len(e.Line(c.Row))
		if e.Mode() != Insert && c.Col > n-1 && (n != 0 || c.Col != 0) {
			t.Fatalf("step %d: col %d beyond line len %d in %v", i, c.Col, n, e.Mode())
		}
		if c.Col > n {
			t.Fatalf("step %d: col %d beyond line len %d", i, c.Col, n)
		}
		if sel, ok := e.Selection(); ok && posLess(sel.End, sel.Start) {
			t.Fatalf("step %d: unordered selection", i)
		}
	}
}

// TestReviewRegressions pins behaviors checked against vim 9.
func TestReviewRegressions(t *testing.T) {
	runCases(t, []tc{
		{"s on an empty line inserts", "type user\n\ntype document", "js  define x<esc>", "type user\n  define x\ntype document"},
		{"x on an empty line keeps the register", "ab\n", "yljxp", "ab\na"},
		{"visual line op repeats over as many lines", "a\nb\nc\nd", "ddV>.", "    b\nc\nd"},
		{"visual delete repeats", "a\nb\nc\nd\ne\nf\ng", "Vjd.", "e\nf\ng"},
		{"visual char delete repeats over as many chars", "a b c d", "xvld.", " d"},
		{"cb at the buffer start is cancelled", "ab", "cbX<esc>", "ab"},
		{"substitute without a previous pattern", "abc", ":s//X/<enter>", "abc"},
	})
}

func TestStarSkipsTheWordUnderCursor(t *testing.T) {
	e := ed("foo bar foo")
	feed(e, "$#")
	if c := e.Cursor(); c != (Pos{0, 0}) {
		t.Fatalf("# from inside the last foo: cursor %v, want {0 0}", c)
	}
	e = ed("x  foo bar foo")
	feed(e, "l*")
	if c := e.Cursor(); c != (Pos{0, 11}) {
		t.Fatalf("* from before foo: cursor %v, want {0 11}", c)
	}
}

func TestDotRepeatsAMultiLinePaste(t *testing.T) {
	e := ed("x\ny")
	feed(e, "o")
	e.InsertText("a\nb")
	feed(e, "<esc>j.")
	if got, want := e.Value(), "x\na\nb\ny\na\nb"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if e.LineCount() != 6 {
		t.Fatalf("LineCount %d disagrees with Value", e.LineCount())
	}
}

func TestSubstituteWithoutPatternSaysSo(t *testing.T) {
	e := ed("abc")
	feed(e, ":s//X/<enter>")
	if e.Status() != "No previous regular expression" {
		t.Fatalf("status %q", e.Status())
	}
}

// TestCopilotReviewRegressions pins behaviors checked against vim 9.
func TestCopilotReviewRegressions(t *testing.T) {
	runCases(t, []tc{
		{"count before daw", "foo bar baz", "2daw", "baz"},
		{"count inside daw", "foo bar baz", "d2aw", "baz"},
		{"count on diw takes the blank run", "foo bar", "2diw", "bar"},
		{"daw count past the line fails", "foo bar", "3daw", "foo bar"},
		{"visual count on aw", "foo bar baz", "v2awd", "baz"},
		{"count picks the enclosing pair", "a (b (c) d) e", "fcd2i(", "a () e"},
		{"iw from linewise visual is charwise", "foo bar", "Viwd", " bar"},
		{"brace interior selects whole lines", "x {\n  a\n  b\n}", "jvi{d", "x {\n}"},
		{"empty pair leaves the selection", "()", "vi(d", ")"},
		{"empty quotes select both, as vim", "\"\"", "vi\"d", ""},
		{"interior ending at column 0 keeps the close", "(a\n)", "lvi(d", "(\n)"},
		{"dot count sticks", "a b c d e f g", "dw2..", "f g"},
		{"s// reuses the substitute pattern", "foo foo", ":s/foo/bar/<enter>:s//baz/<enter>", "bar baz"},
	})
}

// TestSearchPastEndOfLineAdvances: /$ matches past the last rune, where the
// cursor can't sit; n must still move on to the next line.
func TestSearchPastEndOfLineAdvances(t *testing.T) {
	e := ed("abc\ndef")
	feed(e, "/$<enter>n")
	if c := e.Cursor(); c != (Pos{1, 2}) {
		t.Fatalf("cursor %v, want {1 2}", c)
	}
}

// TestDotRepeatsTheOpeningInsert: the editor opens in Insert mode, and what
// is typed there is a change "." repeats.
func TestDotRepeatsTheOpeningInsert(t *testing.T) {
	e := New()
	e.SetValue("x")
	feed(&e, "ab<esc>.")
	if got := e.Value(); got != "aabbx" {
		t.Fatalf("got %q, want %q", got, "aabbx")
	}
}
