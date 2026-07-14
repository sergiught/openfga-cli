package apilog

import (
	"sync"
	"testing"
)

func TestRecorderRingEvictsOldest(t *testing.T) {
	r := NewRecorder(2)
	r.Add(Entry{URL: "a"})
	r.Add(Entry{URL: "b"})
	r.Add(Entry{URL: "c"})
	got := r.Snapshot()
	if len(got) != 2 || got[0].URL != "b" || got[1].URL != "c" {
		t.Fatalf("want [b c], got %+v", got)
	}
}

func TestSnapshotIsIndependentCopy(t *testing.T) {
	r := NewRecorder(4)
	r.Add(Entry{URL: "a"})
	snap := r.Snapshot()
	snap[0].URL = "mutated"
	if r.Snapshot()[0].URL != "a" {
		t.Fatal("Snapshot must return an independent copy")
	}
}

func TestClearEmpties(t *testing.T) {
	r := NewRecorder(4)
	r.Add(Entry{URL: "a"})
	r.Clear()
	if len(r.Snapshot()) != 0 {
		t.Fatal("Clear must empty the buffer")
	}
}

func TestNotifyFiresOnAddAndIsNilSafe(t *testing.T) {
	r := NewRecorder(4)
	r.Add(Entry{}) // nil notify must not panic
	var mu sync.Mutex
	n := 0
	r.SetNotify(func() { mu.Lock(); n++; mu.Unlock() })
	r.Add(Entry{})
	r.Add(Entry{})
	mu.Lock()
	defer mu.Unlock()
	if n != 2 {
		t.Fatalf("want 2 notifications, got %d", n)
	}
}
