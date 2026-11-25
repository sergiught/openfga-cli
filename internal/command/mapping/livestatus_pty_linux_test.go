package mapping

import (
	"errors"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

func TestBackfillDeclinedQuestionDrawsNothingAfterIt(t *testing.T) {
	configtest.Isolate(t)
	useCard(t)
	pinNow(t, 6e9)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	master, tty := openPTY(t)
	if _, err := master.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	w, err := runTogether(t, fs.cli(), tty, "backfill", mp, "--provider", "fake")
	if !errors.Is(err, prompt.ErrAborted) {
		t.Fatalf("err = %v", err)
	}
	read := w.indexOf(0, "read in ")
	ask := w.indexOf(read, "Write 4 tuples to store acme (")
	if read < 0 || ask < 0 {
		t.Fatalf("want the read line, then the question:\n%s", w.text())
	}
	w.noCardAfter(t, read)
	if strings.Contains(w.text(), "wrote in ") {
		t.Errorf("a declined question wrote:\n%s", w.text())
	}
}
