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
func renderDrift(w io.Writer, run *snapshotRun, storeLabel string, storeReadAt time.Time, stored int, diff snapshot.Diff, notChecked []snapshot.NotChecked) error {
	label := output.SanitizeField(run.Provider.Label())
	layout := snapshotLayout(run.Result)
	if storeReadAt.UTC().Format(time.DateOnly) != run.Result.FinishedAt.UTC().Format(time.DateOnly) {
		layout = dateClockLayout // the store was read the day before the provider
	}
	_, _ = fmt.Fprintf(w, "store read  %s UTC  %s  %s\n", storeReadAt.UTC().Format(layout), mapping.Plural(stored, "tuple"), output.SanitizeField(storeLabel))
	snapshotHeader(w, run.Provider.Label(), run.Result)

	// A stale tuple is shown as the delete it would become.
	stale := make([]language.Tuple, len(diff.Stale))
	for i, t := range diff.Stale {
		t.Action = language.ActionDelete
		stale[i] = t
	}
	for _, s := range []struct {
		name, what string
		tuples     []language.Tuple
	}{
		{"missing", "in " + label + ", not in the store", diff.Missing},
		{"changed", "condition differs", diff.Changed},
		{"stale", "in the store, not in " + label, stale},
	} {
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
