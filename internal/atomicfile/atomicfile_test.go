package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommitReplacesTheDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.json")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Create(dest, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
	assertOnlyFile(t, dir, "out.json")
}

// The whole point of staging: a caller that gives up must leave the previous
// contents exactly as they were.
func TestAbortLeavesTheDestinationUntouched(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.json")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Create(dest, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	f.Abort()

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Errorf("contents = %q, want %q", got, "old")
	}
	assertOnlyFile(t, dir, "out.json")
}

// Callers defer Abort and Commit on the success path, so the two run in
// sequence on every successful write.
func TestAbortAfterCommitIsANoOp(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.json")

	f, err := Create(dest, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("kept")); err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(); err != nil {
		t.Fatal(err)
	}
	f.Abort()

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "kept" {
		t.Errorf("contents = %q, want %q", got, "kept")
	}
}

// A staged file starts at 0600 from os.CreateTemp; the requested mode has to
// survive to the destination.
func TestCommitAppliesTheRequestedMode(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "report.json")

	f, err := Create(dest, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 644", perm)
	}
}

// Staging must not create the destination's directory: a mistyped path should
// still fail rather than quietly materialise a tree.
func TestCreateFailsWhenTheDirectoryIsMissing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nope", "out.json")
	if _, err := Create(dest, 0o600); err == nil {
		t.Fatal("staging into a missing directory should fail")
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		got := make([]string, 0, len(entries))
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Errorf("directory contains %v, want only %q", got, name)
	}
}

func TestCommitNewRefusesAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(dest, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Create(dest, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("mine")); err != nil {
		t.Fatal(err)
	}
	if err := f.CommitNew(); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CommitNew = %v, want fs.ErrExist", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "theirs" {
		t.Errorf("contents = %q, want %q", got, "theirs")
	}
	assertOnlyFile(t, dir, "out.yaml")
}

func TestCommitNewWritesAFreeDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.yaml")

	f, err := Create(dest, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("mine")); err != nil {
		t.Fatal(err)
	}
	if err := f.CommitNew(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mine" {
		t.Errorf("contents = %q, want %q", got, "mine")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dest)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("perm = %v, want 0644", info.Mode().Perm())
		}
	}
	assertOnlyFile(t, dir, "out.yaml")
}
