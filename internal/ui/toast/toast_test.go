package toast

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestToastLifecycle(t *testing.T) {
	m := New()
	cmd := m.Push(Success, "store created")
	if !m.Active() || !strings.Contains(stripAnsi(m.View()), "store created") {
		t.Fatal("toast should be visible after Push")
	}
	if cmd == nil {
		t.Fatal("Push must return an expiry cmd")
	}
	m.Update(expireMsg{id: m.nextID}) // expire the toast we just pushed
	if m.Active() {
		t.Fatal("toast should expire")
	}
}

func TestErrorToastExpires(t *testing.T) {
	// Error toasts linger longer than info, but must still auto-expire — a
	// sticky error toast would clutter the screen forever with no way to dismiss.
	m := New()
	cmd := m.Push(Error, "boom")
	if cmd == nil {
		t.Fatal("error toast must return an expiry cmd, not be sticky")
	}
	m.Update(expireMsg{id: m.nextID})
	if m.Active() {
		t.Fatal("error toast should expire")
	}
}

func TestToastTextWidthCap(t *testing.T) {
	m := New()
	longMsg := "This is a very long error message that would normally overflow and break the layout if not truncated properly with ellipsis at the end to ensure it fits"
	if len(longMsg) < 100 {
		t.Fatal("test message must be 100+ chars")
	}
	m.Push(Error, longMsg)
	w := lipgloss.Width(m.View())
	if w > 62 {
		t.Fatalf("toast width should be at most 62, got %d", w)
	}
}

// stripAnsi removes CSI sequences for assertion purposes.
func stripAnsi(s string) string {
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
