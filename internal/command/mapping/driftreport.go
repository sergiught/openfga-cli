package mapping

import (
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

// renderDrift prints the human report: when the store was read, the snapshot
// header, each kind of drift with its tuples, and what was not checked.
func renderDrift(w io.Writer, run *snapshotRun, storeLabel string, storeReadAt time.Time, stored, objects int, afterProvider bool, diff snapshot.Diff, notChecked []snapshot.NotChecked) error {
	label := output.SanitizeField(run.Provider.Label())
	// The store line follows the provider header when the store was read after
	// the provider, and is left out when it was not read at all.
	if !afterProvider {
		storeReadLine(w, run, storeLabel, storeReadAt, stored, 0)
	}
	snapshotHeader(w, run.Provider.Label(), run.Result, run.Since)
	if afterProvider && !storeReadAt.IsZero() {
		storeReadLine(w, run, storeLabel, storeReadAt, stored, objects)
	}

	// A stale tuple is shown as the delete it would become.
	stale := make([]language.Tuple, len(diff.Stale))
	for i, t := range diff.Stale {
		t.Action = language.ActionDelete
		stale[i] = t
	}
	if err := renderSections(w, []tupleSection{
		{"missing", "in " + label + ", not in the store", diff.Missing},
		{"changed", "condition differs", diff.Changed},
		{"stale", "in the store, not in " + label, stale},
	}); err != nil {
		return err
	}

	if len(notChecked) == 0 {
		return nil
	}
	width := 0
	for _, n := range notChecked {
		width = max(width, utf8.RuneCountInString(n.Rule))
	}
	_, _ = fmt.Fprintf(w, "\nnot checked\n")
	for _, n := range notChecked {
		if _, err := fmt.Fprintf(w, "  %-*s  %s\n", width, output.SanitizeField(n.Rule), output.SanitizeField(n.Reason)); err != nil {
			return err
		}
	}
	return nil
}

// storeReadLine prints when the store was read and how many tuples it held.
// A nonzero objects means only those objects were read, so the count is of the
// tuples they held.
func storeReadLine(w io.Writer, run *snapshotRun, storeLabel string, storeReadAt time.Time, stored, objects int) {
	layout := snapshotLayout(run.Result)
	if storeReadAt.UTC().Format(time.DateOnly) != run.Result.FinishedAt.UTC().Format(time.DateOnly) {
		layout = dateClockLayout // the store was read on another day than the provider finished
	}
	count := mapping.Plural(stored, "tuple")
	if objects > 0 {
		count += " on " + mapping.Plural(objects, "object")
	}
	_, _ = fmt.Fprintf(w, "store read  %s UTC  %s  %s\n", storeReadAt.UTC().Format(layout), count, output.SanitizeField(storeLabel))
}

// tupleSection is one named list of tuples in a report.
type tupleSection struct {
	name, what string
	tuples     []language.Tuple
}

// renderSections prints each non-empty section after a blank line.
func renderSections(w io.Writer, sections []tupleSection) error {
	for _, s := range sections {
		if len(s.tuples) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n%-7s  %s\n", s.name, s.what); err != nil {
			return err
		}
		if err := renderTuples(w, s.tuples); err != nil {
			return err
		}
	}
	return nil
}
