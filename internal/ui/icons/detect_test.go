package icons

import (
	"os"
	"path/filepath"
	"testing"
)

// envFrom builds an os.Getenv stand-in from a map so detect() can be driven
// without touching the real environment.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectDisqualifiers(t *testing.T) {
	// A directory that would otherwise promote to nerdfont, proving each
	// disqualifier wins over a positive font signal.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "FiraCodeNerdFont-Regular.ttf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		env   map[string]string
		isTTY bool
	}{
		{"not a tty", map[string]string{"TERM": "xterm-256color"}, false},
		{"empty TERM", map[string]string{}, true},
		{"dumb TERM", map[string]string{"TERM": "dumb"}, true},
		{"linux console", map[string]string{"TERM": "linux"}, true},
		{"ssh connection", map[string]string{"TERM": "xterm-256color", "SSH_CONNECTION": "10.0.0.1 22"}, true},
		{"ssh tty", map[string]string{"TERM": "xterm-256color", "SSH_TTY": "/dev/pts/3"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detect(envFrom(tc.env), []string{dir}, tc.isTTY); got != ModeUnicode {
				t.Fatalf("detect() = %v, want ModeUnicode", got)
			}
		})
	}
}

func TestDetectSSHBeatsWezTerm(t *testing.T) {
	// TERM_PROGRAM survives some SSH setups but describes the wrong machine.
	env := envFrom(map[string]string{
		"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm", "SSH_CONNECTION": "10.0.0.1 22",
	})
	if got := detect(env, nil, true); got != ModeUnicode {
		t.Fatalf("detect() = %v, want ModeUnicode", got)
	}
}

func TestDetectWezTerm(t *testing.T) {
	env := envFrom(map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"})
	if got := detect(env, nil, true); got != ModeNerdFont {
		t.Fatalf("detect() = %v, want ModeNerdFont", got)
	}
}

func TestDetectFontFile(t *testing.T) {
	base := envFrom(map[string]string{"TERM": "xterm-256color"})

	t.Run("nested nerd font promotes", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "truetype", "jetbrains")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "JetBrainsMono Nerd Font Mono.ttf"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detect(base, []string{dir}, true); got != ModeNerdFont {
			t.Fatalf("detect() = %v, want ModeNerdFont", got)
		}
	})

	t.Run("plain fonts stay unicode", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "DejaVuSansMono.ttf"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detect(base, []string{dir}, true); got != ModeUnicode {
			t.Fatalf("detect() = %v, want ModeUnicode", got)
		}
	})

	t.Run("missing dirs are not an error", func(t *testing.T) {
		if got := detect(base, []string{filepath.Join(t.TempDir(), "nope")}, true); got != ModeUnicode {
			t.Fatalf("detect() = %v, want ModeUnicode", got)
		}
	})

	t.Run("depth cap stops the walk", func(t *testing.T) {
		dir := t.TempDir()
		deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deep, "SomethingNerdFont.ttf"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detect(base, []string{dir}, true); got != ModeUnicode {
			t.Fatal("walk should not descend past scanMaxDepth")
		}
	})
}

func TestIsNerdFontName(t *testing.T) {
	yes := []string{
		"FiraCodeNerdFont-Regular.ttf",
		"Symbols Nerd Font.otf",
		"JetBrainsMono Nerd Font Mono.ttf",
		"Hack_Nerd_Font.ttc",
		"caskaydiacove nerd font.OTF",
	}
	for _, n := range yes {
		if !isNerdFontName(n) {
			t.Fatalf("isNerdFontName(%q) = false, want true", n)
		}
	}
	no := []string{
		"DejaVuSansMono.ttf",
		"FiraCode-Regular.ttf",
		// Right name, but not a font file.
		"NerdFont-readme.txt",
		"nerdfont.md",
	}
	for _, n := range no {
		if isNerdFontName(n) {
			t.Fatalf("isNerdFontName(%q) = true, want false", n)
		}
	}
}

func TestDetectIsSafeToCall(t *testing.T) {
	// Detect() reads the real environment; it must never panic or return an
	// off rung regardless of where the suite runs.
	if got := Detect(); got != ModeNerdFont && got != ModeUnicode {
		t.Fatalf("Detect() = %v, want nerdfont or unicode", got)
	}
}
