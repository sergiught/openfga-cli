// Package atomicfile writes a file by staging it next to its destination and
// renaming it into place only once the caller is satisfied with the contents.
//
// Commands that write a result file (`tuples read --output-file`, `model test
// --report-file`) used to open the destination with O_TRUNC before the work
// that produces the content had even started. That makes the destination a
// casualty of any later failure: pointing an export at an existing file and
// then losing the connection destroyed the previous contents, and a failure
// partway through left a truncated file that looks complete. Staging means the
// destination keeps its old contents until a complete result is ready to
// replace them.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is an io.Writer that accumulates into a temporary file in the
// destination's directory — the same filesystem, so the final rename is atomic.
// Commit (or CommitNew) writes the destination; Abort discards the staged data.
// Exactly one of them should run, and Abort is a no-op after a successful
// Commit or CommitNew, so the usual shape is a deferred Abort plus a Commit on
// the success path.
type File struct {
	tmp  *os.File
	name string // staged path, empty once committed or aborted
	dest string
	perm os.FileMode
}

// Create stages a replacement for dest. The destination's directory must
// already exist — staging deliberately does not create it, so a mistyped path
// still fails instead of quietly materialising a tree. perm is applied to the
// finished file, overriding the 0600 that os.CreateTemp uses while staging.
func Create(dest string, perm os.FileMode) (*File, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("stage %s: %w", dest, err)
	}
	return &File{tmp: tmp, name: tmp.Name(), dest: dest, perm: perm}, nil
}

// Write appends to the staged file.
func (f *File) Write(p []byte) (int, error) { return f.tmp.Write(p) }

// Commit flushes the staged file and renames it over the destination. The
// flush is what makes a full disk (ENOSPC, EDQUOT, a deferred NFS commit)
// surface as an error here instead of silently leaving a short file behind.
func (f *File) Commit() error {
	name, err := f.seal()
	if err != nil || name == "" {
		return err
	}
	if err := os.Rename(name, f.dest); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", f.dest, err)
	}
	return nil
}

// CommitNew is Commit for a destination that must not exist: it links the
// staged file into place, so a file that appears at dest after staging began is
// never replaced. That case fails with an error wrapping fs.ErrExist and leaves
// dest untouched. A filesystem that cannot hard link (any link error other than
// fs.ErrExist) falls back to an Lstat of dest followed by a rename, which
// keeps the check but not the guarantee: a file created between the two is
// replaced. On failure the staged file is removed; after a successful link a
// failed remove is ignored, because the destination is complete.
func (f *File) CommitNew() error {
	name, err := f.seal()
	if err != nil || name == "" {
		return err
	}
	err = os.Link(name, f.dest)
	if err == nil {
		// dest now holds the inode, so a failure to drop the staged name
		// leaves nothing wrong with the result.
		_ = os.Remove(name)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", f.dest, err)
	}
	if _, serr := os.Lstat(f.dest); serr == nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", f.dest, fs.ErrExist)
	}
	if err := os.Rename(name, f.dest); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", f.dest, err)
	}
	return nil
}

// seal syncs, closes and chmods the staged file and returns its name, which the
// caller must move into place or remove. It returns "" when already committed
// or aborted; on error the staged file is already removed.
func (f *File) seal() (string, error) {
	if f.name == "" {
		return "", nil
	}
	name := f.name
	f.name = ""

	if err := f.tmp.Sync(); err != nil {
		_ = f.tmp.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("write %s: %w", f.dest, err)
	}
	if err := f.tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("write %s: %w", f.dest, err)
	}
	if err := os.Chmod(name, f.perm); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("secure %s: %w", f.dest, err)
	}
	return name, nil
}

// Abort discards the staged file, leaving the destination untouched. It is a
// no-op once Commit or CommitNew has run.
func (f *File) Abort() {
	if f.name == "" {
		return
	}
	name := f.name
	f.name = ""
	_ = f.tmp.Close()
	_ = os.Remove(name)
}
