package vimedit

import (
	"strings"
	"testing"
)

var tokens = map[string]Key{
	"esc":    {Name: "esc"},
	"enter":  {Name: "enter"},
	"bs":     {Name: "backspace"},
	"del":    {Name: "delete"},
	"tab":    {Name: "tab", Text: "\t"},
	"space":  {Name: "space", Text: " "},
	"up":     {Name: "up"},
	"down":   {Name: "down"},
	"left":   {Name: "left"},
	"right":  {Name: "right"},
	"home":   {Name: "home"},
	"end":    {Name: "end"},
	"pgup":   {Name: "pgup"},
	"pgdown": {Name: "pgdown"},
	"c-d":    {Name: "ctrl+d"},
	"c-u":    {Name: "ctrl+u"},
	"c-f":    {Name: "ctrl+f"},
	"c-b":    {Name: "ctrl+b"},
	"c-r":    {Name: "ctrl+r"},
	"c-w":    {Name: "ctrl+w"},
	"lt":     {Name: "<", Text: "<"},
}

// feed types a vim-style key sequence, returning the last action that was not None.
func feed(e *Editor, seq string) Action {
	var last Action
	rs := []rune(seq)
	for i := 0; i < len(rs); i++ {
		var k Key
		if rs[i] == '<' {
			if j := strings.IndexRune(string(rs[i:]), '>'); j > 0 {
				name := string(rs[i+1 : i+len([]rune(string(rs[i:])[:j]))])
				if t, ok := tokens[name]; ok {
					k = t
					i += len([]rune(name)) + 1
					if a := e.HandleKey(k); a != None {
						last = a
					}
					continue
				}
			}
		}
		s := string(rs[i])
		k = Key{Name: s, Text: s}
		if s == " " {
			k.Name = "space"
		}
		if a := e.HandleKey(k); a != None {
			last = a
		}
	}
	return last
}

// ed returns an editor in Normal mode holding text, cursor at (0,0).
func ed(text string) *Editor {
	e := New()
	e.SetValue(text)
	e.HandleKey(Key{Name: "esc"})
	return &e
}

type tc struct {
	name, in, keys, want string
}

func runCases(t *testing.T, cases []tc) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ed(c.in)
			feed(e, c.keys)
			if got := e.Value(); got != c.want {
				t.Fatalf("keys %q on %q:\n got %q\nwant %q", c.keys, c.in, got, c.want)
			}
		})
	}
}

func cursorIs(t *testing.T, e *Editor, row, col int) {
	t.Helper()
	if got := e.Cursor(); got != (Pos{row, col}) {
		t.Fatalf("cursor = %+v, want {%d %d}", got, row, col)
	}
}
