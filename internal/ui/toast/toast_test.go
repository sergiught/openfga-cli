package toast

import (
	"strings"
	"testing"
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
	m.Update(expireMsg{id: m.id}) // exported for test via same package
	if m.Active() {
		t.Fatal("toast should expire")
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
