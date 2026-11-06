package filelock

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		u, err := Lock(path)
		if err == nil {
			u()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("second Lock returned (%v) while the first was held", err)
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Lock did not acquire after the first was released")
	}
}
