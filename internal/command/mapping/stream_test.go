package mapping

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// userFailMapping fails on a user event: the field it reads is not there.
const userFailMapping = `version: "1"
rules:
  - name: user
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.nope.id }}"
        relation: member
        object: "organization:o1"
`

func TestStreamedReadGivesTheSameResultsAsAReturnedOne(t *testing.T) {
	for _, cmd := range []string{"drift", "backfill"} {
		t.Run(cmd, func(t *testing.T) {
			configtest.Isolate(t)

			type outcome struct{ stdout, stderr, writes string }
			run := func(stream bool) outcome {
				fs := newFakeStore(t)
				useFakeProvider(t, fakeProvider{types: fakeTypes(), result: blockedResult(), stream: stream})
				args := []string{writeFile(t, t.TempDir(), "m.yaml", blockedMapping), "--provider", "fake", "--force"}
				stdout, stderr, err := runSnapshotCmd(t, fs.cli(), nil, cmd, args...)
				if err != nil {
					t.Fatalf("%s stream=%v: err = %v\n%s", cmd, stream, err, stderr)
				}
				return outcome{stdout, stderr, strings.Join(fs.writes, "\n")}
			}

			plain, streamed := run(false), run(true)
			if !strings.Contains(plain.stdout, "NOT CHECKED") || !strings.Contains(plain.writes, "user:u2") || strings.Contains(plain.writes, "user:u1") {
				t.Fatalf("returned events: %+v", plain)
			}
			if plain != streamed {
				t.Errorf("streamed read differs:\nreturned: %+v\nstreamed: %+v", plain, streamed)
			}
		})
	}
}

func TestStreamedReadStopsWhenARuleFailsOnAnEvent(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	res := fakeResult()
	res.Events = []map[string]any{
		{"type": "snapshot.user", "subject": "user auth0|anne", "user_id": "u1"},
		{"type": "snapshot.organization.member", "user_id": "u1", "org": "o1"},
	}
	var steps []string
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: res, stream: true, steps: &steps})
	_, stderr, err := runBackfill(t, fs.cli(), nil, writeFile(t, t.TempDir(), "m.yaml", userFailMapping), "--provider", "fake", "--dry-run")
	if err == nil {
		t.Fatalf("no error\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "snapshot.user (user auth0|anne)") {
		t.Errorf("err = %v", err)
	}
	if got := strings.Join(steps, ","); got != "emit snapshot.user,stopped" {
		t.Errorf("steps = %s", got)
	}
}

func TestStreamedBackfillDrawsTheCardWithoutAReplayStep(t *testing.T) {
	configtest.Isolate(t)
	interactiveProgress(t)
	useCard(t)
	pinNow(t, 60e9)
	fs := newFakeStore(t)
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult(), stream: true})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	w, err := runTogether(t, fs.cli(), nil, "backfill", mp, "--provider", "fake", "--force")
	out := w.text()
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	noLegacy(t, out)
	for _, want := range []string{"2 items, 4 tuples from 2 events", "wrote in "} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(ansi.Strip(out), "Replay") {
		t.Errorf("a Replay row is drawn:\n%s", out)
	}
}

func TestCardHasNoReplayRowWhenEventsAreReturned(t *testing.T) {
	for _, cmd := range []string{"drift", "backfill"} {
		t.Run(cmd, func(t *testing.T) {
			configtest.Isolate(t)
			interactiveProgress(t)
			useCard(t)
			fs := newFakeStore(t)
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
			mp := writeFile(t, t.TempDir(), "m.yaml", driftMapping)
			args := []string{mp, "--provider", "fake", "--dry-run"}
			if cmd == "backfill" {
				args = []string{mp, "--provider", "fake", "--force"}
			}
			w, _ := runTogether(t, fs.cli(), nil, cmd, args...)
			out := ansi.Strip(w.text())
			if !strings.Contains(out, "╭─") || strings.Contains(out, "Replay") {
				t.Errorf("output:\n%s", out)
			}
		})
	}
}
