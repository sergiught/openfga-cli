package style

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sergiught/openfga-cli/internal/theme"
)

func TestGradientPreservesText(t *testing.T) {
	// Force TrueColor so lipgloss emits ANSI escape codes even outside a TTY.
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	Apply(theme.Default()) // aurora has gradient endpoints
	out := Gradient("ofga")
	stripped := stripANSI(out)
	if stripped != "ofga" {
		t.Errorf("Gradient visible text = %q, want %q", stripped, "ofga")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("expected ANSI color codes in gradient output")
	}
}

func TestGradientMonoIsPlain(t *testing.T) {
	Apply(theme.Mono())
	if out := Gradient("ofga"); out != "ofga" {
		t.Errorf("mono Gradient = %q, want plain %q", out, "ofga")
	}
	Apply(theme.Default()) // restore for other tests
}

func TestDotRendersGlyph(t *testing.T) {
	Apply(theme.Default())
	for _, st := range []DotState{DotOnline, DotBusy, DotError, DotOffline} {
		if got := stripANSI(Dot(st)); got != IconDot {
			t.Errorf("Dot(%d) glyph = %q, want %q", st, got, IconDot)
		}
	}
}

// stripANSI removes CSI sequences for assertion purposes.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
