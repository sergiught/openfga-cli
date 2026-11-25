package mapping

import (
	"io"
	"strconv"

	"github.com/sergiught/openfga-cli/internal/command/mapping/status"
)

// newStatus builds the live status card; tests replace it.
var newStatus = status.New

// liveStatus returns the status card for a command that draws on w, or nil
// when output is machine-readable or --debug traces requests to stderr.
func (c *Command) liveStatus(w io.Writer, machine bool, title string, phases ...string) *status.Live {
	if machine || c.cli.Tracing() {
		return nil
	}
	return newStatus(w, title, phases...)
}

// matchStatus shows one ReadMatching report on the status card.
func matchStatus(live *status.Live, mp matchProgress) {
	switch mp.Step {
	case "store":
		live.Set("tuples", mp.Done, 0)
	case "objects":
		live.Set("objects", mp.Done, mp.Total)
	case "keys":
		live.Set("tuples by key", mp.Done, mp.Total)
	}
}

// matchNote is what the Store phase shows once ReadMatching is done.
func matchNote(read storeRead) string {
	note := thousands(read.Read) + " tuples read"
	if read.Objects > 0 {
		note += " on " + thousands(read.Objects) + " objects"
	}
	return note
}

// thousands formats n with thousands separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
