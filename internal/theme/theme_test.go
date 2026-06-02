package theme

import "testing"

func TestAuroraRegisteredAndDefault(t *testing.T) {
	a, ok := Get("aurora")
	if !ok {
		t.Fatal(`Get("aurora") not found`)
	}
	if a.GradStartHex != "#00FAFF" || a.GradEndHex != "#8BFF95" {
		t.Errorf("aurora gradient = %q→%q, want #00FAFF→#8BFF95", a.GradStartHex, a.GradEndHex)
	}
	if Default().Name != "aurora" {
		t.Errorf("Default().Name = %q, want aurora", Default().Name)
	}
	found := false
	for _, n := range Names() {
		if n == "aurora" {
			found = true
		}
	}
	if !found {
		t.Error("Names() does not include aurora")
	}
}
