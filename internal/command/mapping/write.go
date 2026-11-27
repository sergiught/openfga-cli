package mapping

import (
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot/workfile"
)

// writePage is how many diff rows go to the store between two MarkSent calls.
const writePage = 10_000

// sendPages walks the diff rows of one kind in pages and hands each to step,
// which sends it and reports what reached the store. prior is how many rows
// the pages before this one held. A page is marked sent only when step returned
// without an error, so the rows of a failed page are sent again by the next
// run. step's error comes back as it is, with what was sent so far.
func sendPages(f *workfile.File, kind workfile.DiffKind, step func(page, was []language.Tuple, prior int) (snapshot.Sent, error)) (snapshot.Sent, error) {
	var total snapshot.Sent
	prior := 0
	err := f.Pages(kind, writePage, func(page, was []language.Tuple) error {
		sent, err := step(page, was, prior)
		total.Writes += sent.Writes
		total.Deletes += sent.Deletes
		if err != nil {
			return err
		}
		if err := f.MarkSent(kind, page); err != nil {
			return err
		}
		prior += len(page)
		return nil
	})
	return total, err
}
