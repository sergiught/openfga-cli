package status

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

func sample0() state {
	pal := newPalette()
	return state{
		title: "ofga mappings drift", elapsed: 196 * time.Second, frame: 2,
		phases: []phase{
			{name: "Store", state: done, note: "1,204,300 tuples read · 412,118 kept", took: 62 * time.Second},
			{name: "Auth0", state: running, step: "members of organizations", done: 3120, total: 10000, left: 14 * time.Minute},
			{name: "Replay"}, {name: "Write"},
		},
		sources: []source{
			{name: "Auth0", requests: 3274, limited: 12, rate: 8.1, pace: 120 * time.Millisecond},
			{name: "store", requests: 12043},
		},
		mem: 1181116006, top: 1395864371,
		data: []string{
			pal.bold.Render("214") + pal.subtle.Render(" organizations"),
			pal.bold.Render("311,870") + pal.subtle.Render(" organization members"),
			pal.bold.Render("98,112") + pal.subtle.Render(" roles"),
		},
	}
}

var sgrColor = regexp.MustCompile(`\x1b\[[0-9;]*(3[0-9]|4[0-9]|9[0-7]|10[0-7]|38|48)[0-9;]*m`)

func plain(s string) string { return ansi.Strip(s) }

func checkWidths(t *testing.T, out string, cols int) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > cols-1 {
			t.Errorf("line is %d cells, max %d: %q", w, cols-1, plain(l))
		}
	}
}

func TestViewCard(t *testing.T) {
	out := plain(view(sample0(), 100, 40))
	for _, want := range []string{
		"╭─ ofga mappings drift ", " 3m 16s ─╮", "╰", "✓ Store",
		"1,204,300 tuples read · 412,118 kept", "1m 02s", "⠹ Auth0", "members of organizations",
		"3,120 / 10,000", " 31% · 14m 00s left", "○ Replay", "○ Write",
		"REQUESTS", "RATE LIMITED", "MEMORY", "3,274", "8.1/s", "12,043", "12×", "1 per 120ms",
		"1.1 GB", "peak 1.3 GB",
		"DATA  214 organizations · 311,870 organization members · 98,112 roles",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card lacks %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 78 {
			t.Errorf("card line wider than 78: %q", l)
		}
	}
	checkWidths(t, view(sample0(), 100, 40), 100)
	if n := strings.Count(out, "\n") + 1; n != 17 {
		t.Errorf("card has %d lines, want 17:\n%s", n, out)
	}
}

func TestViewWidths(t *testing.T) {
	for _, cols := range []int{30, 40, 56, 64, 80, 100, 200} {
		checkWidths(t, view(sample0(), cols, 40), cols)
	}
}

func TestViewNarrowStacks(t *testing.T) {
	wide := strings.Split(plain(view(sample0(), 100, 60)), "\n")
	narrow := strings.Split(plain(view(sample0(), 56, 60)), "\n")
	if len(narrow) <= len(wide) {
		t.Fatalf("narrow card has %d lines, wide %d: tiles did not stack", len(narrow), len(wide))
	}
	for _, l := range narrow {
		if strings.Contains(l, "REQUESTS") && (strings.Contains(l, "MEMORY") || strings.Contains(l, "RATE LIMITED")) {
			t.Errorf("tiles share a line when narrow: %q", l)
		}
	}
}

func TestViewShortTerminal(t *testing.T) {
	out := plain(view(sample0(), 100, 10))
	if strings.Contains(out, "REQUESTS") || strings.Contains(out, "╭") || strings.Contains(out, "Replay") {
		t.Errorf("short terminal drew the card:\n%s", out)
	}
	if !strings.Contains(out, "Auth0") || !strings.Contains(out, "3,120 / 10,000") {
		t.Errorf("short terminal lacks the running phase:\n%s", out)
	}
	checkWidths(t, view(sample0(), 100, 10), 100)
}

func TestViewBarOnlyWithKnownTotal(t *testing.T) {
	s := sample0()
	if !strings.Contains(plain(view(s, 100, 40)), "━") {
		t.Error("no bar for a known total")
	}
	s.phases[1].total, s.phases[1].rate = 0, 19400
	out := plain(view(s, 100, 40))
	if strings.Contains(out, "━") || strings.Contains(out, "%") {
		t.Errorf("bar for an unknown total:\n%s", out)
	}
	if !strings.Contains(out, "3,120 · 19,400/s") {
		t.Errorf("no count and rate for an unknown total:\n%s", out)
	}
}

func TestViewMono(t *testing.T) {
	t.Cleanup(func() { style.Apply(theme.Default()) })
	if !style.SetTheme("mono") {
		t.Fatal("no mono theme")
	}
	out := view(sample0(), 100, 40)
	if m := sgrColor.FindString(out); m != "" {
		t.Errorf("color %q in the mono theme: %q", m, out)
	}
	if !strings.Contains(plain(out), "━━━╸") && !strings.Contains(plain(out), "━─") {
		t.Errorf("mono bar is not filled and empty:\n%s", plain(out))
	}
}

func TestViewPhaseStates(t *testing.T) {
	s := sample0()
	s.phases[1] = phase{name: "Auth0"}
	out := plain(view(s, 100, 40))
	for _, want := range []string{"✓ Store", "○ Auth0", "○ Replay"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "⠹") {
		t.Errorf("a spinner with nothing running:\n%s", out)
	}
}

func TestViewMemoryOnly(t *testing.T) {
	s := sample0()
	s.sources, s.data = nil, nil
	out := plain(view(s, 100, 40))
	if strings.Contains(out, "REQUESTS") || strings.Contains(out, "DATA") || !strings.Contains(out, "MEMORY") {
		t.Errorf("unexpected tiles:\n%s", out)
	}
}

func TestViewNeverPanicsOrOverflows(t *testing.T) {
	for cols := 0; cols <= 30; cols++ {
		for rows := 0; rows <= 5; rows++ {
			out := view(sample0(), cols, rows)
			if cols < minCols || rows < 3 {
				if out != "" {
					t.Errorf("cols %d rows %d drew %q", cols, rows, plain(out))
				}
				continue
			}
			checkWidths(t, out, cols)
		}
	}
}

func TestRenderedCards(t *testing.T) {
	wide := `╭─ ofga mappings drift ───────────────────────────────────────────── 3m 16s ─╮
│                                                                            │
│  ✓ Store   1,204,300 tuples read · 412,118 kept                    1m 02s  │
│  ⠹ Auth0   members of organizations                        3,120 / 10,000  │
│            ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━   31% · 14m 00s left  │
│  ○ Replay                                                                  │
│  ○ Write                                                                   │
│                                                                            │
│  ────────────────────────────────────────────────────────────────────────  │
│                                                                            │
│  REQUESTS                  RATE LIMITED                 MEMORY             │
│  Auth0   3,274  8.1/s      Auth0  12×  1 per 120ms      1.1 GB             │
│  store  12,043             store  0×                    peak 1.3 GB        │
│                                                                            │
│  DATA  214 organizations · 311,870 organization members · 98,112 roles     │
│                                                                            │
╰────────────────────────────────────────────────────────────────────────────╯`
	narrow := `╭─ ofga mappings drift ────────────────────── 3m 16s ─╮
│                                                     │
│  ✓ Store   1,204,300 tuples read · 412,118… 1m 02s  │
│  ⠹ Auth0   members of organizations 3,120 / 10,000  │
│            ━━━━━━━━━━━━━━━━━━   31% · 14m 00s left  │
│  ○ Replay                                           │
│  ○ Write                                            │
│                                                     │
│  ─────────────────────────────────────────────────  │
│                                                     │
│  REQUESTS                                           │
│  Auth0   3,274  8.1/s                               │
│  store  12,043                                      │
│                                                     │
│  RATE LIMITED                                       │
│  Auth0  12×  1 per 120ms                            │
│  store  0×                                          │
│                                                     │
│  MEMORY                                             │
│  1.1 GB                                             │
│  peak 1.3 GB                                        │
│                                                     │
│  DATA  214 organizations ·                          │
│        311,870 organization members · 98,112 roles  │
│                                                     │
╰─────────────────────────────────────────────────────╯`
	if got := plain(view(sample0(), 100, 40)); got != wide {
		t.Errorf("card at 100 columns:\n%s", got)
	}
	if got := plain(view(sample0(), 56, 60)); got != narrow {
		t.Errorf("card at 56 columns:\n%s", got)
	}
}

func TestViewDrawsAPendingPhaseBeforeAStartedOneAsSkipped(t *testing.T) {
	s := sample0()
	s.phases = []phase{
		{name: "Store", state: done, note: "5 tuples read", took: time.Second},
		{name: "Write", note: "stale note"},
		{name: "Prune", state: running},
		{name: "Later"},
	}
	out := plain(view(s, 100, 40))
	for _, want := range []string{"– Write", "○ Later", "Prune"} {
		if !strings.Contains(out, want) {
			t.Errorf("card lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "○ Write") || strings.Contains(out, "stale note") {
		t.Errorf("a skipped phase is drawn as pending or with a note:\n%s", out)
	}
}
