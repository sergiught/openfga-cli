package extedit

import "testing"

// TestEditorCommandResolvesEnv checks the $VISUAL/$EDITOR resolution and the
// fallback path. It stays robust to CI by asserting the env path directly and
// only asserting that the fallback doesn't panic (it returns whatever is on
// PATH, or "" when nothing suitable is found).
func TestEditorCommandResolvesEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myed --wait")
	bin, args := Command()
	if bin != "myed" {
		t.Fatalf("EDITOR should resolve to myed, got %q", bin)
	}
	if len(args) != 1 || args[0] != "--wait" {
		t.Fatalf("editor args should be [--wait], got %v", args)
	}

	t.Setenv("VISUAL", "vised")
	if bin, _ := Command(); bin != "vised" {
		t.Fatalf("VISUAL should take precedence, got %q", bin)
	}

	// Neither env set: fallback to a PATH lookup. Just assert it doesn't panic
	// and returns a string (possibly "" on a bare CI box with no editor).
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	_, _ = Command()
}

func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"vim", []string{"vim"}},
		{"code --wait", []string{"code", "--wait"}},
		{"  nvim   -u  none  ", []string{"nvim", "-u", "none"}},
		{`"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl" -w`,
			[]string{"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl", "-w"}},
		{`'my editor' --flag`, []string{"my editor", "--flag"}},
		{`emacs\ w`, []string{"emacs w"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := splitCommandLine(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCommandLine(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCommandLine(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
