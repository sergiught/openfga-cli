package status

import (
	"testing"
	"time"
)

func TestFormats(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1204300: "1,204,300", -1234: "-1,234"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q, want %q", n, got, want)
		}
	}
	for d, want := range map[time.Duration]string{42 * time.Second: "42s", 196 * time.Second: "3m 16s", 2*time.Hour + 5*time.Minute: "2h 05m"} {
		if got := dur(d); got != want {
			t.Errorf("dur(%v) = %q, want %q", d, got, want)
		}
	}
	for b, want := range map[uint64]string{812 << 20: "812 MB", 1181116006: "1.1 GB"} {
		if got := size(b); got != want {
			t.Errorf("size(%d) = %q, want %q", b, got, want)
		}
	}
}

func TestTypeName(t *testing.T) {
	for _, c := range []struct {
		typ  string
		n    int
		want string
	}{
		{"snapshot.organization", 214, "organizations"},
		{"snapshot.organization.member", 3, "organization members"},
		{"snapshot.organization_member", 1, "organization member"},
		{"snapshot.organization.member.role", 2, "organization member roles"},
	} {
		if got := typeName(c.typ, c.n); got != c.want {
			t.Errorf("typeName(%q, %d) = %q, want %q", c.typ, c.n, got, c.want)
		}
	}
}

func TestETA(t *testing.T) {
	if got := eta(2*time.Second, 10, 100); got != 0 {
		t.Errorf("eta before 3s = %v, want 0", got)
	}
	if got := eta(10*time.Second, 0, 100); got != 0 {
		t.Errorf("eta with nothing done = %v, want 0", got)
	}
	if got := eta(10*time.Second, 25, 100); got != 30*time.Second {
		t.Errorf("eta = %v, want 30s", got)
	}
}

func TestWindowRate(t *testing.T) {
	var w window
	t0 := time.Unix(0, 0)
	w.add(t0, 0)
	if w.rate() != 0 {
		t.Error("rate with one sample")
	}
	w.add(t0.Add(500*time.Millisecond), 5) // within the second: ignored
	w.add(t0.Add(2*time.Second), 20)
	if got := w.rate(); got != 10 {
		t.Errorf("rate = %v, want 10", got)
	}
	for i := 3; i <= 10; i++ { // only the last 5 seconds count
		w.add(t0.Add(time.Duration(i)*time.Second), 20+(i-2)*100)
	}
	if got := w.rate(); got != 100 {
		t.Errorf("rate = %v, want 100", got)
	}
}
