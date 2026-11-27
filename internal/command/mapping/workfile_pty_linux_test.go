package mapping

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

func TestInteractiveKeepPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, answers string
		args          []string
		keep          bool
	}{
		{"keep after a dry run", "y\n", []string{"--dry-run"}, true},
		{"empty answer deletes", "\n", []string{"--dry-run"}, false},
		{"declined write then keep", "n\ny\n", nil, true},
		{"declined write then delete", "n\n\n", nil, false},
	} {
		for _, name := range []string{"backfill", "drift"} {
			t.Run(name+" "+tc.name, func(t *testing.T) {
				configtest.Isolate(t)
				fs := newFakeStore(t)
				useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1")})
				state := filepath.Join(t.TempDir(), "w.db")
				master, tty := openPTY(t)
				if _, err := master.WriteString(tc.answers + "\n"); err != nil {
					t.Fatal(err)
				}
				_, stderr, _ := runSnapshotCmd(t, fs.cli(), tty, name, stateArgs(t, driftMapping, state, tc.args...)...)
				question := fmt.Sprintf("Keep the work file at %s to reuse? [y/N]: ", state)
				if strings.Count(stderr, question) != 1 {
					t.Errorf("want the keep question once:\n%s", stderr)
				}
				if tc.args == nil && strings.Index(stderr, question) < strings.Index(stderr, "[y/N]") {
					t.Errorf("the keep question came before the write question:\n%s", stderr)
				}
				if exists(state) != tc.keep {
					t.Errorf("file exists = %v, want %v", exists(state), tc.keep)
				}
				if fs.writeCount() != 0 {
					t.Errorf("writes = %v", fs.writes)
				}
			})
		}
	}
}
