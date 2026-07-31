package style

import "testing"

// SanitizeTerminalKeepSGR must keep colour (which only paints) and drop
// anything that acts on the terminal, so a line the CLI deliberately styled
// survives while an escape injected into an interpolated value does not.
func TestSanitizeTerminalKeepSGR(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"keeps SGR", "\x1b[1mbold\x1b[0m", "\x1b[1mbold\x1b[0m"},
		{"keeps truecolor SGR", "\x1b[38;2;1;2;3mx\x1b[m", "\x1b[38;2;1;2;3mx\x1b[m"},
		{"keeps bare reset", "a\x1b[mb", "a\x1b[mb"},
		{"drops OSC 52 clipboard write", "a\x1b]52;c;evil\x07b", "ab"},
		{"drops OSC terminated by ST", "a\x1b]0;title\x1b\\b", "ab"},
		{"drops screen clear", "a\x1b[2Jb", "ab"},
		{"drops cursor position", "a\x1b[10;10Hb", "ab"},
		{"drops control characters", "a\x07\x00b", "ab"},
		// The C1 CSI introducer is removed, which defuses the sequence; its
		// parameter bytes are then ordinary text and stay, matching
		// SanitizeTerminal.
		{"drops C1 introducer", "a\u009b31mb", "a31mb"},
		{"drops bidi override", "doc\u202egnp", "docgnp"},
		{"drops zero-width space", "ad\u200bmin", "admin"},
		{"lone ESC at end", "ab\x1b", "ab"},
		{"truncated CSI", "ab\x1b[38;2", "ab"},
		{"two-byte escape", "a\x1bMb", "ab"},
		{"plain text untouched", "user:anne viewer doc:1", "user:anne viewer doc:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeTerminalKeepSGR(tt.in); got != tt.want {
				t.Errorf("SanitizeTerminalKeepSGR(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
