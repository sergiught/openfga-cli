package mapping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/openfga/mapper/language"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/output"
)

const applyStoreID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

const membership = `version: "1"
rules:
  - name: added
    when: input.type == "added"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
  - name: removed
    when: input.type == "removed"
    action: delete
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
  - name: roster
    when: input.type == "roster"
    tuple_filters:
      - relation: member
        object: "team:{{ input.team }}"
    iterator:
      source: input.users
      as: u
      tuples:
        - user: "user:{{ u }}"
          relation: member
          object: "team:{{ input.team }}"
`

// conditional writes a tuple with a context; mapper renders context values as
// strings, so a stored numeric context differs and forces delete + write.
const conditional = `version: "1"
rules:
  - name: grant
    when: input.type == "grant"
    tuple_filters:
      - relation: member
        object: "team:{{ input.team }}"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
        condition: in_office
        context:
          level: "{{ input.level }}"
`

const twoEvents = `{"type":"added","user":"u1","team":"t1"}
{"type":"other"}
`

// fakeStore is an in-memory OpenFGA: /read answers from tuples, /write is
// recorded, GET /stores/{id} names the store.
type fakeStore struct {
	srv    *httptest.Server
	mu     sync.Mutex
	tuples []map[string]any // each is a tuple key object
	writes []string
	// failWrite makes every /write answer 400 with this body when set.
	failWrite string
}

func newFakeStore(t *testing.T, stored ...map[string]any) *fakeStore {
	t.Helper()
	fs := &fakeStore{tuples: stored}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/stores/"+applyStoreID):
			_, _ = io.WriteString(w, `{"id":"`+applyStoreID+`","name":"acme"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/read"):
			var req struct {
				TupleKey map[string]string `json:"tuple_key"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var out []map[string]any
			for _, k := range fs.tuples {
				if matchesKey(k, req.TupleKey) {
					out = append(out, map[string]any{"key": k})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tuples": out, "continuation_token": ""})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/write"):
			b, _ := io.ReadAll(r.Body)
			fs.writes = append(fs.writes, string(b))
			if fs.failWrite != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, fs.failWrite)
				return
			}
			_, _ = io.WriteString(w, "{}")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func matchesKey(k map[string]any, f map[string]string) bool {
	for field, want := range f {
		got, _ := k[field].(string)
		if want == "" {
			continue
		}
		if strings.HasSuffix(want, ":") {
			if !strings.HasPrefix(got, want) {
				return false
			}
		} else if got != want {
			return false
		}
	}
	return true
}

func (fs *fakeStore) writeCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.writes)
}

func (fs *fakeStore) cli() *cli.CLI {
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: fs.srv.URL, StoreID: applyStoreID})
	return cli.New(log.New(io.Discard), cfg, "test")
}

// runApply executes `mappings apply` keeping stdout and stderr apart.
func runApply(t *testing.T, ctx context.Context, c *cli.CLI, stdin io.Reader, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := New(c).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetArgs(append([]string{"apply"}, args...))
	root.SilenceUsage, root.SilenceErrors = true, true
	err = root.ExecuteContext(ctx)
	return out.String(), errb.String(), err
}

func applyFiles(t *testing.T, mapping, events string) (mappingPath, eventsPath string) {
	t.Helper()
	dir := t.TempDir()
	return writeFile(t, dir, "mapping.yaml", mapping), writeFile(t, dir, "events.jsonl", events)
}

func TestApplyDryRunShowsThePlanAndWritesNothing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	stdout, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, want := range []string{"event 1 · added", "+ user:u1 member team:t1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "2 events: 1 write, 0 deletes, 1 matched no rule") {
		t.Errorf("stderr missing summary:\n%s", stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatalf("dry run sent %d writes", fs.writeCount())
	}
}

func TestApplyRefusesWithoutConfirmation(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	_, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep)
	if err == nil || !strings.Contains(err.Error(), "refusing to proceed without confirmation; pass --force") {
		t.Fatalf("err = %v", err)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote without confirmation")
	}
}

func TestApplyForceWritesExactlyThePlan(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 1 || !strings.Contains(fs.writes[0], `"on_duplicate":"ignore"`) || !strings.Contains(fs.writes[0], "user:u1") {
		t.Fatalf("writes = %v", fs.writes)
	}
	if !strings.Contains(stderr, "sent 1 write and 0 deletes across 2 events to store acme") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestApplyMachineOutputNeverPrompts(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	c := fs.cli()
	c.JSON = true
	_, _, err := runApply(t, context.Background(), c, nil, mp, "--events", ep)
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}

	stdout, stderr, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	var got applyResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !got.DryRun || got.Writes != 1 || got.Events != 2 || got.Unmatched != 1 || len(got.Changes) != 1 ||
		got.Changes[0].Event != 1 || got.Changes[0].Action != "write" || got.Changes[0].User != "user:u1" {
		t.Fatalf("result = %+v", got)
	}
}

func TestApplyReadsEventsFromStdin(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, _ := applyFiles(t, membership, "")
	_, stderr, err := runApply(t, context.Background(), fs.cli(), strings.NewReader(twoEvents), mp, "--events", "-", "--force")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if strings.Contains(stderr, "reading events") || fs.writeCount() != 1 {
		t.Fatalf("writes = %d stderr:\n%s", fs.writeCount(), stderr)
	}
}

func TestApplyNothingToChangeNeedsNoConfirmation(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, `{"type":"other"}`)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep)
	if err != nil || !strings.Contains(stderr, "nothing to change") {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote")
	}
}

func TestApplyReportsHowFarItGot(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	mp, ep := applyFiles(t, membership, twoEvents)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if clierr.Code(err) != clierr.CodeError || !clierr.IsSilent(err) {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if got := clierr.SilentSummary(err); !strings.HasPrefix(got, "no events applied; event 1 failed: ") || clierr.Classify(err) != clierr.KindValidation {
		t.Errorf("summary=%q kind=%v", got, clierr.Classify(err))
	}
	if !strings.Contains(stderr, "no events applied; event 1 failed: ") || !strings.Contains(stderr, "type not found") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "event 1: ") {
		t.Errorf("event prefix doubled:\n%s", stderr)
	}
	t.Run("machine output keeps stderr clean", func(t *testing.T) {
		c := fs.cli()
		c.JSON = true
		_, stderr, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--force")
		// The pre-apply plan summary is progress, not the failure report.
		if !clierr.IsSilent(err) || strings.Contains(stderr, "failed") {
			t.Fatalf("err=%v stderr=%q", err, stderr)
		}
	})
	// Nothing got through, so running the same command again is the resume.
	if strings.Contains(stderr, "resume") {
		t.Errorf("a run that applied nothing offers a resume:\n%s", stderr)
	}
}

// A resumed run that fails at once still needs its --from-event: running it
// again without one would replay the events before it.
func TestAResumedRunThatAppliesNothingKeepsItsResume(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	mp, ep := applyFiles(t, membership, `{"type":"added","user":"u1","team":"t1"}
{"type":"added","user":"u2","team":"t1"}
`)
	_, stderr, _ := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--from-event", "2", "--force")
	if want := "resume with: ofga mappings apply " + mp + " --events " + ep + " --from-event 2 --force"; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

func TestApplyReportsTheAppliedRangeOnAFailureLaterOn(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership,
		`{"type":"added","user":"u1","team":"t1"}`+"\n"+`{"type":"added","user":"u2","team":"t1"}`+"\n"+`{"type":"added","user":"u3","team":"t1"}`+"\n")
	fs.srv.Config.Handler = failNthWrite(fs.srv.Config.Handler, 2)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stderr, "applied event 1; event 2 failed: ") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// failNthWrite makes the nth POST .../write answer 400.
func failNthWrite(next http.Handler, n int) http.Handler {
	var mu sync.Mutex
	seen := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/write") {
			mu.Lock()
			seen++
			fail := seen == n
			mu.Unlock()
			if fail {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":"validation_error","message":"nope"}`)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func TestApplyEmptyEventsFile(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, "")
	_, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if clierr.Code(err) != clierr.CodeError || err == nil || !strings.Contains(err.Error(), "no events in") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyMissingEventsFile(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, _ := applyFiles(t, membership, "")
	missing := filepath.Join(t.TempDir(), "nope.jsonl")
	_, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", missing, "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "events file "+missing+" not found") || clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyStopsOnACompileErrorBeforeAnyRequest(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, "version: \"1\"\nrules: [\n", twoEvents)
	stdout, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if clierr.Code(err) != clierr.CodeError || !clierr.IsSilent(err) || !strings.Contains(stdout, "mapping.yaml:2:8") {
		t.Fatalf("err = %v stdout:\n%s", err, stdout)
	}
}

func TestApplySanitizesTerminalControlsInTheUser(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, `{"type":"added","user":"u\u001b[31m1","team":"t1"}`)
	stdout, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "\x1b") || !strings.Contains(stdout, "member team:t1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestApplyCancelledContext(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runApply(t, ctx, fs.cli(), nil, mp, "--events", ep, "--force")
	// A pre-cancelled context stops in planning, before anything is sent.
	if !errors.Is(err, context.Canceled) || clierr.Code(err) != clierr.CodeCanceled {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if fs.writeCount() != 0 {
		t.Fatal("wrote after cancel")
	}
}

func TestApplyCancelledWhileExecutingReportsTheRange(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership,
		`{"type":"added","user":"u1","team":"t1"}`+"\n"+`{"type":"added","user":"u2","team":"t1"}`+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel as the second write arrives: the first event has landed.
	h := fs.srv.Config.Handler
	var mu sync.Mutex
	writes := 0
	fs.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/write") {
			mu.Lock()
			writes++
			if writes == 2 {
				cancel()
			}
			mu.Unlock()
		}
		if strings.HasSuffix(r.URL.Path, "/write") && writes == 2 {
			// Hold the request until the client has seen the cancel (the
			// server only notices a dropped connection once the body is read).
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		h.ServeHTTP(w, r)
	})
	_, _, err := runApply(t, ctx, fs.cli(), nil, mp, "--events", ep, "--force")
	var partial *clierr.PartialResult
	if !errors.Is(err, context.Canceled) || clierr.Code(err) != clierr.CodeCanceled || !errors.As(err, &partial) {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if !strings.Contains(err.Error(), "applied event 1; stopped before event 2 finished") || strings.Contains(err.Error(), "failed") ||
		!strings.Contains(err.Error(), "resume with: ofga mappings apply "+mp+" --events "+ep+" --from-event 2 --force") {
		t.Errorf("err = %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"sync/auth0.yaml": "sync/auth0.yaml",
		"my events.jsonl": "'my events.jsonl'",
		"it's.yaml":       `'it'\''s.yaml'`,
		"":                "''",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestApplyShowsSplitEventsInTheHeader(t *testing.T) {
	configtest.Isolate(t)
	// Stored with a numeric level; mapper renders "5", so the tuple is
	// deleted and rewritten: two requests.
	fs := newFakeStore(t, map[string]any{
		"user": "user:u1", "relation": "member", "object": "team:t1",
		"condition": map[string]any{"name": "in_office", "context": map[string]any{"level": 5}},
	})
	mp, ep := applyFiles(t, conditional, `{"type":"grant","user":"u1","team":"t1","level":5}`)
	stdout, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	for _, want := range []string{"event 1 · grant (split into 2 requests)", "- user:u1 member team:t1 (in_office: level=5)", `+ user:u1 member team:t1 (in_office: level="5")`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestApplyPromptPluralizes(t *testing.T) {
	cases := []struct {
		writes, deletes int
		want            string
	}{
		{1, 0, "Apply 1 write to store acme (" + applyStoreID + ")?"},
		{3, 0, "Apply 3 writes to store acme (" + applyStoreID + ")?"},
		{0, 1, "Apply 1 delete to store acme (" + applyStoreID + ")?"},
		{1, 4, "Apply 1 write and 4 deletes to store acme (" + applyStoreID + ")?"},
	}
	for _, tc := range cases {
		if got := applyQuestion(tc.writes, tc.deletes, "acme", applyStoreID); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}

func TestApplyCapsThePlan(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	var events strings.Builder
	for i := range 60 {
		fmt.Fprintf(&events, `{"type":"added","user":"u%d","team":"t1"}`+"\n", i)
	}
	mp, ep := applyFiles(t, membership, events.String())
	stdout, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "… 10 more; --dry-run -o json lists them all") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

const threeEvents = `{"type":"added","user":"u1","team":"t1"}
{"type":"added","user":"u2","team":"t1"}
{"type":"added","user":"u3","team":"t1"}
`

func TestApplyReportsARangeOfAppliedEvents(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, threeEvents)
	fs.srv.Config.Handler = failNthWrite(fs.srv.Config.Handler, 3)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(stderr, "applied events 1-2; event 3 failed: ") {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
}

func TestApplyYAMLNeverPrompts(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	c := fs.cli()
	c.YAML = true
	_, _, err := runApply(t, context.Background(), c, nil, mp, "--events", ep)
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
}

func TestApplyJSONOnFailureIncludesAppliedEvents(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, threeEvents)
	fs.srv.Config.Handler = failNthWrite(fs.srv.Config.Handler, 3)
	c := fs.cli()
	c.JSON = true
	stdout, _, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--force")
	var got applyResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil || got.AppliedEvents != 2 || got.DryRun {
		t.Fatalf("stdout=%s jerr=%v", stdout, jerr)
	}
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
}

// readGuard fails the test if anything reads it.
type readGuard struct{ t *testing.T }

func (g readGuard) Read([]byte) (int, error) {
	g.t.Error("stdin was read")
	return 0, io.EOF
}

func TestApplyStdinStillNeedsForce(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	var requests int
	next := fs.srv.Config.Handler
	fs.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		next.ServeHTTP(w, r)
	})
	mp, _ := applyFiles(t, membership, "")
	// Piped events use up stdin, so nothing could answer the prompt: refuse
	// before reading them or asking the store anything.
	_, _, err := runApply(t, context.Background(), fs.cli(), readGuard{t}, mp, "--events", "-")
	if err == nil || err.Error() != "refusing to proceed without confirmation; pass --force to skip the prompt" || requests != 0 {
		t.Fatalf("err = %v requests = %d", err, requests)
	}
	if _, _, err := runApply(t, context.Background(), fs.cli(), strings.NewReader(twoEvents), mp, "--events", "-", "--dry-run"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
}

func TestApplyUnreachableServerFailsBeforePlanning(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	c := fs.cli()
	fs.srv.Close()
	mp, ep := applyFiles(t, membership, twoEvents)
	stdout, stderr, err := runApply(t, context.Background(), c, nil, mp, "--events", ep)
	if clierr.Code(err) != clierr.CodeNetwork {
		t.Fatalf("code = %d err = %v", clierr.Code(err), err)
	}
	if stdout != "" || strings.Contains(stderr, "matched no rule") || strings.Contains(err.Error(), "refusing") {
		t.Fatalf("planned or prompted:\n%s\n%s\n%v", stdout, stderr, err)
	}
}

func TestMachineModeKeepsProblemLinesOffStdout(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, "version: \"1\"\nrules: [\n", twoEvents)
	for _, args := range [][]string{
		{"apply", mp, "--events", ep, "--dry-run"},
		{"test", mp},
	} {
		c := fs.cli()
		c.JSON = true
		root := New(c).Command()
		var out, errb bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errb)
		root.SetArgs(args)
		root.SilenceUsage, root.SilenceErrors = true, true
		err := root.Execute()
		// The error envelope is the only thing main writes to stderr under
		// machine output, so the problem lines move into its details.
		env := clierr.NewEnvelope(err).Error
		if clierr.Code(err) != clierr.CodeError || out.Len() != 0 || errb.Len() != 0 ||
			env.Code != clierr.KindValidation || len(env.Details) == 0 || env.Details[0]["line"] != 2 {
			t.Errorf("%v: err=%v stdout=%q stderr=%q envelope=%+v", args, err, out.String(), errb.String(), env)
		}
	}
}

func TestApplyFromEventSkipsEarlierEvents(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, threeEvents)
	stdout, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--from-event", "2", "--dry-run")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if strings.Contains(stdout, "event 1 ") || strings.Contains(stdout, "user:u1") ||
		!strings.Contains(stdout, "event 2 · added") || !strings.Contains(stdout, "event 3 · added") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "2 events: 2 writes, 0 deletes") {
		t.Errorf("stderr:\n%s", stderr)
	}

	c := fs.cli()
	c.JSON = true
	stdout, _, err = runApply(t, context.Background(), c, nil, mp, "--events", ep, "--from-event", "2", "--dry-run")
	if err != nil || !strings.Contains(stdout, `"from_event": 2`) || !strings.Contains(stdout, `"event": 2`) {
		t.Fatalf("err = %v stdout:\n%s", err, stdout)
	}
	stdout, _, _ = runApply(t, context.Background(), c, nil, mp, "--events", ep, "--dry-run")
	if strings.Contains(stdout, "from_event") {
		t.Errorf("from_event shown for a full run:\n%s", stdout)
	}
}

func TestApplyFromEventOutOfRangeIsAUsageError(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, threeEvents)
	for _, n := range []string{"0", "4"} {
		_, _, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--from-event", n, "--dry-run")
		if clierr.Code(err) != clierr.CodeUsage || err == nil || err.Error() != "--from-event "+n+": "+ep+" has 3 events" {
			t.Errorf("--from-event %s: code = %d err = %v", n, clierr.Code(err), err)
		}
	}
}

func TestApplyFailurePrintsTheResumeCommand(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, threeEvents)
	fs.srv.Config.Handler = failNthWrite(fs.srv.Config.Handler, 2)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	if want := "resume with: ofga mappings apply " + mp + " --events " + ep + " --from-event 2 --force"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
}

func TestApplyResumedRunReportsAbsoluteEventNumbers(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, _ := applyFiles(t, membership, "")
	fs.srv.Config.Handler = failNthWrite(fs.srv.Config.Handler, 2)
	_, stderr, err := runApply(t, context.Background(), fs.cli(), strings.NewReader(threeEvents), mp, "--events", "-", "--from-event", "2", "--force")
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"applied event 2; event 3 failed: ", "--events - --from-event 3 --force"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestResumeCommandRepeatsTheStoreFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "apply"}
	cmd.Flags().String("store-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("header", "", "")
	for name, v := range map[string]string{"store-id": "01X", "header": "Authorization: secret"} {
		if err := cmd.Flags().Set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := resumeCommand(cmd, "m.yaml", "-", 3, false), "ofga mappings apply m.yaml --events - --from-event 3 --store-id 01X"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestChangeLineShowsConditionContext(t *testing.T) {
	for _, tc := range []struct {
		t    language.Tuple
		want string
	}{
		{language.Tuple{User: "user:u", Relation: "r", Object: "o:1"}, "+ user:u r o:1"},
		{language.Tuple{User: "user:u", Relation: "r", Object: "o:1", Condition: "c"}, "+ user:u r o:1 (c)"},
		{language.Tuple{User: "user:u", Relation: "r", Object: "o:1", Action: language.ActionDelete, Condition: "c",
			Context: map[string]any{"b": "x\x1b[31my", "a": float64(2), "l": []any{"p", "q"}}},
			`- user:u r o:1 (c: a=2, b="x\u001b[31my", l=["p","q"])`},
	} {
		if got := changeLine(tc.t, output.SanitizeField); got != tc.want {
			t.Errorf("got  %s\nwant %s", got, tc.want)
		}
	}
}

func TestApplyHintsAtAConditionConflict(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, ep := applyFiles(t, membership, twoEvents)
	next := fs.srv.Config.Handler
	fs.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/write") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"aborted","message":"transactional write failed due to conflict: attempted to write a tuple which already exists with a different condition: user: 'user:u1', relation: 'member', object: 'team:t1'"}`)
	})
	_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	want := "a tuple with that user, relation and object already exists with a different condition; a rule without a " +
		"tuple_filter cannot replace it — add a tuple_filter, or delete the old tuple first"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the hint:\n%s", stderr)
	}
}

func TestResumeHintSaysWhatItCannotCarry(t *testing.T) {
	cmd := &cobra.Command{Use: "apply"}
	cmd.Flags().String("header", "", "")
	if got := resumeHint(cmd, "e.jsonl", "ofga x"); got != "resume with: ofga x" {
		t.Fatalf("file events: %q", got)
	}
	if got := resumeHint(cmd, "-", "ofga x"); got != "resume by feeding the same events to: ofga x" {
		t.Fatalf("stdin events: %q", got)
	}
	if err := cmd.Flags().Set("header", "Authorization: secret"); err != nil {
		t.Fatal(err)
	}
	if got := resumeHint(cmd, "e.jsonl", "ofga x"); !strings.HasSuffix(got, "(plus the --header/--auth-* flags you used)") || strings.Contains(got, "secret") {
		t.Fatalf("header set: %q", got)
	}
}

// Ctrl-C only cancels the context, so reading events from a stdin that never
// reaches EOF must give up when it does.
func TestApplyStopsReadingStdinWhenCanceled(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	mp, _ := applyFiles(t, membership, "")
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, _, err := runApply(t, ctx, fs.cli(), r, mp, "--events", "-", "--dry-run")
		got <- err
	}()
	cancel()
	select {
	case err := <-got:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("apply still reading stdin after cancel")
	}
}

// Under machine output the split-event warning belongs in the envelope: with
// nothing applied the kind alone would tell an agent nothing was written.
func TestApplyMachineEnvelopeCarriesSplitEventHint(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, map[string]any{
		"user": "user:u1", "relation": "member", "object": "team:t1",
		"condition": map[string]any{"name": "in_office", "context": map[string]any{"level": 5}},
	})
	fs.failWrite = `{"code":"validation_error","message":"type not found"}`
	mp, ep := applyFiles(t, conditional, `{"type":"grant","user":"u1","team":"t1","level":5}`)
	c := fs.cli()
	c.JSON = true
	_, stderr, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--force")
	if !clierr.IsSilent(err) || strings.Contains(stderr, "failed") {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	env := clierr.NewEnvelope(err)
	if env.Error.Code != clierr.KindValidation {
		t.Errorf("kind = %q", env.Error.Code)
	}
	if !strings.Contains(env.Error.Hint, "event 1 goes out in several requests and may be partly written") {
		t.Errorf("hint = %q", env.Error.Hint)
	}
}

// failSecondWrite answers every /write after the first with fail, retries
// included; the first lands.
func failSecondWrite(fs *fakeStore, fail http.HandlerFunc) {
	h := fs.srv.Config.Handler
	var mu sync.Mutex
	writes := 0
	fs.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/write") {
			mu.Lock()
			writes++
			n := writes
			mu.Unlock()
			if n >= 2 {
				fail(w, r)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func TestApplyMachineEnvelopeKeepsAPIErrorAfterPartialApply(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	failSecondWrite(fs, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"validation_error","message":"type not found"}`)
	})
	mp, ep := applyFiles(t, membership,
		`{"type":"added","user":"u1","team":"t1"}`+"\n"+`{"type":"added","user":"u2","team":"t1"}`+"\n")
	c := fs.cli()
	c.JSON = true
	_, _, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--force")
	env := clierr.NewEnvelope(err)
	if env.Error.Code != clierr.KindPartialFailure || env.Error.Status != http.StatusBadRequest || env.Error.APICode != "validation_error" {
		t.Errorf("envelope = %+v, want partial_failure with status 400 and api_code validation_error", env.Error)
	}
	if !strings.Contains(env.Error.Message, "event 2 failed") {
		t.Errorf("message = %q", env.Error.Message)
	}
}

func TestApplyLostServerAfterPartialApplyIsNetwork(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t)
	failSecondWrite(fs, func(w http.ResponseWriter, _ *http.Request) {
		if conn, _, herr := w.(http.Hijacker).Hijack(); herr == nil {
			_ = conn.Close()
		}
	})
	mp, ep := applyFiles(t, membership,
		`{"type":"added","user":"u1","team":"t1"}`+"\n"+`{"type":"added","user":"u2","team":"t1"}`+"\n")
	c := fs.cli()
	c.JSON = true
	_, _, err := runApply(t, context.Background(), c, nil, mp, "--events", ep, "--force")
	if got := clierr.Code(err); got != clierr.CodeNetwork {
		t.Errorf("exit code = %d, want %d", got, clierr.CodeNetwork)
	}
	if env := clierr.NewEnvelope(err); env.Error.Code != clierr.KindNetwork {
		t.Errorf("kind = %q, want network", env.Error.Code)
	}
}
