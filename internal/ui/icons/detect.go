package icons

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"
)

// Bounds on the font-directory walk. A font tree is normally shallow and
// small, but these keep a pathological one (a network mount, a huge shared
// /usr/share/fonts) from stalling TUI startup.
const (
	scanMaxDepth   = 4
	scanMaxEntries = 4000
)

// Detect guesses a glyph rung from the environment. It returns only
// ModeNerdFont or ModeUnicode: ModeOff is a deliberate user choice and is
// never auto-selected.
//
// No reliable Nerd Font probe exists — the cursor-position trick measures cell
// advance width, not glyph presence, and a missing glyph still advances one
// cell. So this only ever promotes from the safe unicode baseline on a
// positive signal. The errors are asymmetric: guessing nerdfont wrongly prints
// "?" on every tab, while guessing unicode wrongly costs slightly plainer
// icons.
//
// The result is memoized because two entry points (main and the playground's
// launch) each resolve the same auto rung, and the font-directory walk behind
// it is not free. Nothing the guess depends on — env, TTY-ness, installed
// fonts — can meaningfully change within one process.
func Detect() Mode {
	detectOnce.Do(func() {
		detected = detect(os.Getenv, fontDirs(), term.IsTerminal(os.Stdout.Fd()))
	})
	return detected
}

var (
	detectOnce sync.Once
	detected   Mode
)

func detect(env func(string) string, dirs []string, isTTY bool) Mode {
	if !isTTY {
		return ModeUnicode
	}
	switch env("TERM") {
	case "", "dumb", "linux":
		// The bare Linux VT console has no font fallback at all.
		return ModeUnicode
	}
	// Over SSH the font that matters belongs to the *local* terminal. Anything
	// installed on this host says nothing about it, so refuse to guess. This is
	// checked before TERM_PROGRAM, which some setups forward from the client.
	if env("SSH_CONNECTION") != "" || env("SSH_TTY") != "" {
		return ModeUnicode
	}
	// WezTerm bundles Symbols Nerd Font and falls back to it automatically.
	// Imperfect — the fallback is reported to break under some configured
	// fonts (wezterm#5404) — but it is the strongest signal available.
	if env("TERM_PROGRAM") == "WezTerm" {
		return ModeNerdFont
	}
	if hasNerdFontFile(dirs) {
		return ModeNerdFont
	}
	return ModeUnicode
}

// fontDirs returns the platform's font directories, most user-specific first
// so a per-user install is found before walking the system tree.
func fontDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		var dirs []string
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		}
		return append(dirs, "/Library/Fonts", "/System/Library/Fonts")
	case "windows":
		var dirs []string
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			dirs = append(dirs, filepath.Join(d, "Microsoft", "Windows", "Fonts"))
		}
		if d := os.Getenv("WINDIR"); d != "" {
			dirs = append(dirs, filepath.Join(d, "Fonts"))
		}
		return dirs
	default:
		var dirs []string
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			dirs = append(dirs, filepath.Join(d, "fonts"))
		}
		if home != "" {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "fonts"),
				filepath.Join(home, ".fonts"))
		}
		return append(dirs, "/usr/local/share/fonts", "/usr/share/fonts")
	}
}

// hasNerdFontFile reports whether any font file under dirs looks like a Nerd
// Font. Matching is on the filename because that is the only metadata
// reachable without parsing font tables, and parsing every installed font to
// answer a cosmetic question is not worth the startup cost.
func hasNerdFontFile(dirs []string) bool {
	budget := scanMaxEntries
	for _, d := range dirs {
		if scanDir(d, 0, &budget) {
			return true
		}
	}
	return false
}

func scanDir(dir string, depth int, budget *int) bool {
	if depth > scanMaxDepth || *budget <= 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing or unreadable is the common case, not an error: most systems
		// have only a subset of these directories.
		return false
	}
	var subdirs []string
	for _, e := range entries {
		if *budget <= 0 {
			return false
		}
		*budget--
		if e.IsDir() {
			subdirs = append(subdirs, filepath.Join(dir, e.Name()))
			continue
		}
		if isNerdFontName(e.Name()) {
			return true
		}
	}
	// Files before subdirectories: a hit in this directory avoids descending.
	for _, sd := range subdirs {
		if scanDir(sd, depth+1, budget) {
			return true
		}
	}
	return false
}

// isNerdFontName matches the several naming conventions Nerd Fonts ship under
// — "FiraCodeNerdFont-Regular.ttf", "Symbols Nerd Font.otf",
// "Hack_Nerd_Font.ttc" — by folding away separators before the substring test.
// The extension check keeps READMEs and licence files in a font bundle from
// counting as evidence.
func isNerdFontName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc", ".otc":
	default:
		return false
	}
	n := strings.ToLower(name)
	n = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(n)
	return strings.Contains(n, "nerdfont")
}
