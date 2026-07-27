package tuple

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/output"
)

// outputModes are the four renderings every bulk result has to produce.
var outputModes = []string{"json", "yaml", "plain", "human"}

// bulkKinds enumerates the two bulk commands. They share a code path but differ
// in the /write field the tuples land in and the key the result is reported
// under.
var bulkKinds = []struct {
	name   string
	newCmd func(*Command) *cobra.Command
	field  string   // result key: written / deleted
	verb   string   // human-mode success verb
	args   []string // extra flags the command needs
	sent   func(openfga.WriteRequest) (sent, unused *openfga.WriteRequestTuples)
}{
	{
		name: "write", newCmd: (*Command).writeCmd, field: "written", verb: "wrote",
		sent: func(b openfga.WriteRequest) (*openfga.WriteRequestTuples, *openfga.WriteRequestTuples) {
			return b.Writes, b.Deletes
		},
	},
	{
		name: "delete", newCmd: (*Command).deleteCmd, field: "deleted", verb: "deleted",
		args: []string{"--force"},
		sent: func(b openfga.WriteRequest) (*openfga.WriteRequestTuples, *openfga.WriteRequestTuples) {
			return b.Deletes, b.Writes
		},
	},
}

// writeRecorder is a mock OpenFGA /write endpoint that records every request
// body, tracks how many were in flight at once, and can reject the requests
// selected by fail.
type writeRecorder struct {
	mu          sync.Mutex
	bodies      []string
	inFlight    int
	maxInFlight int

	// fail decides whether request n (1-based) is answered with a 400.
	fail func(n int, body string) bool
	// delay holds each request open long enough for concurrency to be visible.
	delay time.Duration
}

func newWriteRecorder(t *testing.T) (*writeRecorder, string) {
	t.Helper()
	rec := &writeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route rather than accept everything: a regression that lost store
		// scoping (POST /stores//write) or posted to the wrong endpoint would
		// otherwise still be recorded here, and every test below would pass
		// against a request the real server answers with a 404.
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/write") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(body))
		n := len(rec.bodies)
		rec.inFlight++
		if rec.inFlight > rec.maxInFlight {
			rec.maxInFlight = rec.inFlight
		}
		fail := rec.fail != nil && rec.fail(n, string(body))
		delay := rec.delay
		rec.mu.Unlock()

		time.Sleep(delay)

		rec.mu.Lock()
		rec.inFlight--
		rec.mu.Unlock()

		if fail {
			http.Error(w, `{"code":"write_failed_due_to_invalid_input","message":"tuple already exists"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return rec, srv.URL
}

// requests returns a copy of the recorded bodies.
func (r *writeRecorder) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

// decoded returns the recorded bodies decoded as write requests.
func (r *writeRecorder) decoded(t *testing.T) []openfga.WriteRequest {
	t.Helper()
	bodies := r.requests()
	out := make([]openfga.WriteRequest, 0, len(bodies))
	for _, b := range bodies {
		var req openfga.WriteRequest
		if err := json.Unmarshal([]byte(b), &req); err != nil {
			t.Fatalf("decode request body %q: %v", b, err)
		}
		out = append(out, req)
	}
	return out
}

// tupleKeysOf decodes the writes (or deletes) block of a recorded body.
func tupleKeysOf(t *testing.T, body string) []openfga.TupleKey {
	t.Helper()
	var req openfga.WriteRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	if req.Writes != nil {
		return req.Writes.TupleKeys
	}
	if req.Deletes != nil {
		return req.Deletes.TupleKeys
	}
	return nil
}

// newBulkCLI builds a CLI rendering in the given output mode.
func newBulkCLI(t *testing.T, apiURL, mode string) *cli.CLI {
	t.Helper()
	configtest.Isolate(t)
	a := newHumanTupleCLI(t, apiURL)
	switch mode {
	case "json":
		a.JSON = true
	case "yaml":
		a.YAML = true
	}
	// Set unconditionally rather than only for "plain": the human-mode cases
	// assert that nothing reaches stdout, which would silently break if some
	// other test in the package ever leaked output.Plain = true.
	output.Plain = mode == "plain"
	t.Cleanup(func() { output.Plain = false })

	return a
}

// silenced points a command's stdout and stderr at buffers and returns them.
// Usage and error rendering are suppressed the way the real root command does
// it, so stdout carries only the command's own output.
func silenced(cmd *cobra.Command) (out, errOut *strings.Builder) {
	out, errOut = &strings.Builder{}, &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return out, errOut
}

// bulkFileOf writes n tuples to a temp file in the given format and returns the
// path. The users are numbered so the request bodies can be matched back to
// their position in the file.
func bulkFileOf(t *testing.T, n int, format bulkFormat) string {
	t.Helper()
	keys := make([]openfga.TupleKey, 0, n)
	for i := range n {
		keys = append(keys, openfga.TupleKey{User: fmt.Sprintf("user:%d", i), Relation: "viewer", Object: "doc:1"})
	}
	p := filepath.Join(t.TempDir(), "tuples."+string(format))
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := encodeTupleFile(f, keys, format); err != nil {
		t.Fatal(err)
	}
	return p
}

// decodeStructured decodes a --json or --yaml payload into a map.
func decodeStructured(t *testing.T, mode, out string) map[string]any {
	t.Helper()
	m := map[string]any{}
	var err error
	if mode == "yaml" {
		err = yaml.Unmarshal([]byte(out), &m)
	} else {
		err = json.Unmarshal([]byte(out), &m)
	}
	if err != nil {
		t.Fatalf("decode %s output %q: %v", mode, out, err)
	}

	return m
}

// numField reads a numeric field from decoded output (JSON decodes numbers as
// float64, YAML as int).
func numField(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("field %q = %#v, want a number", key, m[key])
		return 0
	}
}

func TestBulkSplitsIntoChunksOfHundred(t *testing.T) {
	// The sizes are spelled out rather than derived from the default: OpenFGA's
	// per-request write limit is part of the contract with the server, so
	// changing it has to be a deliberate, test-visible change.
	const total = 101
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec, apiURL := newWriteRecorder(t)
			cmd := k.newCmd(New(newBulkCLI(t, apiURL, "json")))
			cmd.SetArgs(append([]string{"--file", bulkFileOf(t, total, formatJSON)}, k.args...))
			out, _ := silenced(cmd)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			bodies := rec.decoded(t)
			wantSizes := []int{100, 1}
			if len(bodies) != len(wantSizes) {
				t.Fatalf("request count = %d, want %d", len(bodies), len(wantSizes))
			}
			var users []string
			for i, body := range bodies {
				sent, unused := k.sent(body)
				if unused != nil && len(unused.TupleKeys) > 0 {
					t.Errorf("request %d carried %d tuple(s) in the wrong field", i+1, len(unused.TupleKeys))
				}
				if sent == nil {
					t.Fatalf("request %d carried no tuples in the %s field", i+1, k.field)
				}
				if len(sent.TupleKeys) != wantSizes[i] {
					t.Errorf("request %d carried %d tuple(s), want %d", i+1, len(sent.TupleKeys), wantSizes[i])
				}
				for _, key := range sent.TupleKeys {
					users = append(users, key.User)
				}
			}
			if len(users) != total {
				t.Fatalf("sent %d tuple(s) in all, want %d", len(users), total)
			}
			for i, user := range users {
				if want := fmt.Sprintf("user:%d", i); user != want {
					t.Fatalf("tuple %d user = %q, want %q (file order must be preserved)", i, user, want)
				}
			}
			if got := numField(t, decodeStructured(t, "json", out.String()), k.field); got != total {
				t.Errorf("%s = %d, want %d", k.field, got, total)
			}
		})
	}
}

func TestBulkPartialFailureReportsTallyAndRuntimeError(t *testing.T) {
	// The first chunk commits, the second is rejected.
	const total, committed = 101, 100
	for _, k := range bulkKinds {
		for _, mode := range outputModes {
			t.Run(k.name+"/"+mode, func(t *testing.T) {
				rec, apiURL := newWriteRecorder(t)
				rec.fail = func(n int, _ string) bool { return n >= 2 }
				cmd := k.newCmd(New(newBulkCLI(t, apiURL, mode)))
				cmd.SetArgs(append([]string{"--file", bulkFileOf(t, total, formatJSON)}, k.args...))
				out, errOut := silenced(cmd)

				err := cmd.Execute()
				if err == nil {
					t.Fatal("a failing chunk must fail the command")
				}
				if got := clierr.Code(err); got != clierr.CodeError {
					t.Errorf("exit code = %d, want runtime error %d", got, clierr.CodeError)
				}
				// The rejected tuples have already been reported in whichever
				// mode is active, so the error itself is silent — but it still
				// has to carry the partial marker for Ctrl-C reporting.
				var partial *clierr.PartialResult
				if !errors.As(err, &partial) {
					t.Error("a partial bulk failure must carry clierr.PartialResult")
				}

				// The partial tally is emitted before the error propagates.
				switch mode {
				case "json", "yaml":
					m := decodeStructured(t, mode, out.String())
					if got := numField(t, m, k.field); got != committed {
						t.Errorf("%s = %d, want %d", k.field, got, committed)
					}
					if got := numField(t, m, "total"); got != total {
						t.Errorf("total = %d, want %d", got, total)
					}
					if got, ok := m["complete"].(bool); !ok || got {
						t.Errorf("complete = %#v, want false", m["complete"])
					}
				case "plain":
					wantOut := fmt.Sprintf("%s\t%d\ntotal\t%d\ncomplete\tfalse\n", k.field, committed, total)
					if out.String() != wantOut {
						t.Errorf("plain output = %q, want %q", out.String(), wantOut)
					}
				case "human":
					if out.Len() != 0 {
						t.Errorf("human mode wrote to stdout: %q", out.String())
					}
					want := fmt.Sprintf("%s %d, failed %d of %d tuple(s)", k.verb, committed, total-committed, total)
					if !strings.Contains(errOut.String(), want) {
						t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
					}
				}
			})
		}
	}
}

func TestBulkSuccessOutputPerMode(t *testing.T) {
	const total = 2
	for _, k := range bulkKinds {
		for _, mode := range outputModes {
			t.Run(k.name+"/"+mode, func(t *testing.T) {
				rec, apiURL := newWriteRecorder(t)
				cmd := k.newCmd(New(newBulkCLI(t, apiURL, mode)))
				cmd.SetArgs(append([]string{"--file", bulkFileOf(t, total, formatJSON)}, k.args...))
				out, errOut := silenced(cmd)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if got := len(rec.requests()); got != 1 {
					t.Fatalf("request count = %d, want a single request", got)
				}

				switch mode {
				case "json", "yaml":
					if got := numField(t, decodeStructured(t, mode, out.String()), k.field); got != total {
						t.Errorf("%s = %d, want %d", k.field, got, total)
					}
				case "plain":
					if want := fmt.Sprintf("%s\t%d\n", k.field, total); out.String() != want {
						t.Errorf("plain output = %q, want %q", out.String(), want)
					}
				case "human":
					if want := fmt.Sprintf("%s %d tuple(s)", k.verb, total); !strings.Contains(errOut.String(), want) {
						t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
					}
					if out.Len() != 0 {
						t.Errorf("human mode wrote to stdout: %q", out.String())
					}
				}
			})
		}
	}
}

func TestBulkDryRunMakesNoRequest(t *testing.T) {
	const total = 2
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec, apiURL := newWriteRecorder(t)
			cmd := k.newCmd(New(newBulkCLI(t, apiURL, "json")))
			cmd.SetArgs(append([]string{"--dry-run", "--file", bulkFileOf(t, total, formatJSON)}, k.args...))
			out, _ := silenced(cmd)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			if got := len(rec.requests()); got != 0 {
				t.Fatalf("--dry-run issued %d request(s), want none", got)
			}
			m := decodeStructured(t, "json", out.String())
			if got, ok := m["dry_run"].(bool); !ok || !got {
				t.Errorf("dry_run = %#v, want true", m["dry_run"])
			}
			if got := numField(t, m, "would_"+k.name); got != total {
				t.Errorf("would_%s = %d, want %d", k.name, got, total)
			}
		})
	}
}

func TestSingleTupleSentInTheRightField(t *testing.T) {
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec, apiURL := newWriteRecorder(t)
			cmd := k.newCmd(New(newBulkCLI(t, apiURL, "json")))
			cmd.SetArgs(append([]string{"user:anne", "viewer", "doc:1"}, k.args...))
			silenced(cmd)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			bodies := rec.decoded(t)
			if len(bodies) != 1 {
				t.Fatalf("request count = %d, want a single request", len(bodies))
			}
			sent, unused := k.sent(bodies[0])
			if unused != nil && len(unused.TupleKeys) > 0 {
				t.Errorf("tuple landed in the wrong field: %+v", unused.TupleKeys)
			}
			if sent == nil || len(sent.TupleKeys) != 1 {
				t.Fatalf("%s field = %+v, want exactly one tuple", k.field, sent)
			}
			if got := sent.TupleKeys[0].User; got != "user:anne" {
				t.Errorf("user = %q, want %q", got, "user:anne")
			}
		})
	}
}

// TestBulkWriteDefaultsMatchLegacyRequests locks the pre-overhaul contract: with
// no new flags, `tuples write --file x.json` still sends 100-tuple chunks, one
// request at a time, with no on_duplicate field in the body.
func TestBulkWriteDefaultsMatchLegacyRequests(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	rec.delay = 20 * time.Millisecond
	p := bulkFileOf(t, 101, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p})
	silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	bodies := rec.requests()
	if len(bodies) != 2 {
		t.Fatalf("sent %d requests, want 2 (100 + 1)", len(bodies))
	}
	if got := len(tupleKeysOf(t, bodies[0])); got != 100 {
		t.Errorf("first chunk carried %d tuples, want 100", got)
	}
	if got := len(tupleKeysOf(t, bodies[1])); got != 1 {
		t.Errorf("second chunk carried %d tuples, want 1", got)
	}
	if rec.maxInFlight != 1 {
		t.Errorf("max in-flight requests = %d, want 1 (sequential by default)", rec.maxInFlight)
	}
	for i, b := range bodies {
		if strings.Contains(b, "on_duplicate") {
			t.Errorf("request %d must not carry on_duplicate by default: %s", i+1, b)
		}
	}
}

func TestBulkWriteOnDuplicateIgnoreReachesRequest(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	p := bulkFileOf(t, 2, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--on-duplicate", "ignore"})
	silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	bodies := rec.requests()
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"on_duplicate":"ignore"`) {
		t.Fatalf("body should carry on_duplicate=ignore: %v", bodies)
	}
}

func TestBulkDeleteOnMissingIgnoreReachesRequest(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	p := bulkFileOf(t, 2, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).deleteCmd()
	cmd.SetArgs([]string{"--file", p, "--force", "--on-missing", "ignore"})
	silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	bodies := rec.requests()
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"on_missing":"ignore"`) {
		t.Fatalf("body should carry on_missing=ignore: %v", bodies)
	}
}

// TestBulkWriteContinuesPastFailure covers CLI-78: a failing chunk no longer
// aborts the run, the exit code is a runtime error, and the error still carries
// a clierr.PartialResult.
func TestBulkWriteContinuesPastFailure(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	rec.fail = func(n int, _ string) bool { return n == 1 }
	p := bulkFileOf(t, 4, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1"})
	_, errOut := silenced(cmd)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a failed tuple must produce an error")
	}
	if got := clierr.Code(err); got != clierr.CodeError {
		t.Errorf("exit code = %d, want %d (runtime, not usage)", got, clierr.CodeError)
	}
	var partial *clierr.PartialResult
	if !errors.As(err, &partial) {
		t.Error("bulk failure must carry clierr.PartialResult so Ctrl-C reporting keeps working")
	}
	if len(rec.requests()) != 4 {
		t.Errorf("sent %d requests, want 4 — the run must continue past the first failure", len(rec.requests()))
	}
	if !strings.Contains(errOut.String(), "wrote 3, failed 1 of 4") {
		t.Errorf("stderr should carry the summary, got %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "user:0") {
		t.Errorf("stderr should list the failed tuple, got %q", errOut.String())
	}
}

func TestBulkWriteMachineOutputCarriesSuccessfulAndFailed(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	rec.fail = func(n int, _ string) bool { return n == 2 }
	p := bulkFileOf(t, 3, formatJSON)

	a := newHumanTupleCLI(t, apiURL)
	a.JSON = true
	cmd := New(a).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1"})
	out, _ := silenced(cmd)
	if err := cmd.Execute(); err == nil {
		t.Fatal("a failed tuple must produce an error")
	}

	var got struct {
		Written    int                `json:"written"`
		Total      int                `json:"total"`
		Complete   bool               `json:"complete"`
		Successful []openfga.TupleKey `json:"successful"`
		Failed     []struct {
			Tuple  openfga.TupleKey `json:"tuple"`
			Reason string           `json:"reason"`
		} `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if got.Written != 2 || got.Total != 3 || got.Complete {
		t.Errorf("written/total/complete = %d/%d/%v, want 2/3/false", got.Written, got.Total, got.Complete)
	}
	if len(got.Successful) != 2 || len(got.Failed) != 1 {
		t.Fatalf("successful/failed = %d/%d, want 2/1", len(got.Successful), len(got.Failed))
	}
	if got.Failed[0].Tuple.User != "user:1" {
		t.Errorf("failed tuple = %+v, want user:1", got.Failed[0].Tuple)
	}
	if got.Failed[0].Reason == "" {
		t.Error("failed entry must carry a reason")
	}
}

// TestBulkWriteFailedFileRoundTrips checks the whole point of --failed-file:
// the file it writes is directly re-runnable as a --file input.
func TestBulkWriteFailedFileRoundTrips(t *testing.T) {
	for _, format := range []bulkFormat{formatJSON, formatJSONL, formatYAML, formatCSV} {
		t.Run(string(format), func(t *testing.T) {
			rec, apiURL := newWriteRecorder(t)
			rec.fail = func(_ int, body string) bool { return strings.Contains(body, `"user:1"`) }
			p := bulkFileOf(t, 3, format)
			failedPath := filepath.Join(t.TempDir(), "failed."+string(format))

			cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
			cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1", "--failed-file", failedPath})
			silenced(cmd)
			if err := cmd.Execute(); err == nil {
				t.Fatal("a failed tuple must produce an error")
			}

			// Re-run the generated file against a server that accepts everything.
			rec2, apiURL2 := newWriteRecorder(t)
			retry := New(newHumanTupleCLI(t, apiURL2)).writeCmd()
			retry.SetArgs([]string{"--file", failedPath})
			silenced(retry)
			if err := retry.Execute(); err != nil {
				raw, _ := os.ReadFile(failedPath)
				t.Fatalf("re-running --failed-file failed: %v\n%s", err, raw)
			}
			bodies := rec2.requests()
			if len(bodies) != 1 {
				t.Fatalf("retry sent %d requests, want 1", len(bodies))
			}
			keys := tupleKeysOf(t, bodies[0])
			if len(keys) != 1 || keys[0].User != "user:1" {
				t.Fatalf("retry wrote %+v, want just user:1", keys)
			}
		})
	}
}

// TestBulkWriteFailedFileTruncatedOnCleanRun covers the stale-file trap: a
// second run that fixes everything must not leave the first run's failures
// sitting in --failed-file, where a retry loop would replay them forever.
func TestBulkWriteFailedFileTruncatedOnCleanRun(t *testing.T) {
	failedPath := filepath.Join(t.TempDir(), "failed.json")
	p := bulkFileOf(t, 3, formatJSON)

	rec, apiURL := newWriteRecorder(t)
	rec.fail = func(_ int, body string) bool { return strings.Contains(body, `"user:1"`) }
	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1", "--failed-file", failedPath})
	silenced(cmd)
	if err := cmd.Execute(); err == nil {
		t.Fatal("a failed tuple must produce an error")
	}
	if got := failedTuplesIn(t, failedPath); len(got) != 1 {
		t.Fatalf("first run left %d failed tuples, want 1", len(got))
	}

	_, apiURL2 := newWriteRecorder(t)
	retry := New(newHumanTupleCLI(t, apiURL2)).writeCmd()
	retry.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1", "--failed-file", failedPath})
	silenced(retry)
	if err := retry.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := failedTuplesIn(t, failedPath); len(got) != 0 {
		t.Errorf("clean run left %d stale failed tuples in --failed-file, want 0", len(got))
	}
}

// failedTuplesIn parses a --failed-file written in JSON.
func failedTuplesIn(t *testing.T, path string) []tupleInput {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseTupleFile(data, formatJSON, path)
	if err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return got
}

// TestBulkWriteUnreachableServerExitsNetwork covers the scripting contract
// documented in the guide: an unreachable server is exit code 4, not 1, and the
// multi-line "cannot reach" hint is printed once rather than once per tuple.
// The remaining tuples are not attempted — the server will not be up by the
// next chunk — but are still accounted for as failures.
func TestBulkWriteUnreachableServerExitsNetwork(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	apiURL := dead.URL
	dead.Close()

	p := bulkFileOf(t, 4, formatJSON)
	failedPath := filepath.Join(t.TempDir(), "failed.json")

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1", "--failed-file", failedPath})
	_, errOut := silenced(cmd)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an unreachable server must produce an error")
	}
	if got := clierr.Code(err); got != clierr.CodeNetwork {
		t.Errorf("exit code = %d, want %d (network)", got, clierr.CodeNetwork)
	}
	if !strings.Contains(errOut.String(), "wrote 0, failed 4 of 4") {
		t.Errorf("every tuple must be accounted for, got %q", errOut.String())
	}
	if n := strings.Count(errOut.String(), "cannot reach the OpenFGA server"); n != 1 {
		t.Errorf("the network hint appeared %d times, want exactly 1: %q", n, errOut.String())
	}
	if n := strings.Count(errOut.String(), "not attempted"); n != 3 {
		t.Errorf("%d tuples reported as not attempted, want 3 — the run must stop at the first transport failure", n)
	}
	if got := failedTuplesIn(t, failedPath); len(got) != 4 {
		t.Errorf("--failed-file holds %d tuples, want all 4 so the run is re-runnable", len(got))
	}
}

func TestBulkWriteMaxParallelRequestsIsHonoured(t *testing.T) {
	rec, apiURL := newWriteRecorder(t)
	rec.delay = 30 * time.Millisecond
	p := bulkFileOf(t, 12, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1", "--max-parallel-requests", "3"})
	silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(rec.requests()) != 12 {
		t.Fatalf("sent %d requests, want 12", len(rec.requests()))
	}
	rec.mu.Lock()
	peak := rec.maxInFlight
	rec.mu.Unlock()
	if peak > 3 {
		t.Errorf("max in-flight requests = %d, want <= 3", peak)
	}
	if peak < 2 {
		t.Errorf("max in-flight requests = %d, want > 1 with --max-parallel-requests 3", peak)
	}
}

func TestBulkThroughputFlagsRejectNonPositiveValues(t *testing.T) {
	cases := []struct {
		extra []string
		want  string
	}{
		{[]string{"--max-tuples-per-write", "0"}, "--max-tuples-per-write must be at least 1 (got 0)"},
		{[]string{"--max-tuples-per-write", "-1"}, "--max-tuples-per-write must be at least 1 (got -1)"},
		{[]string{"--max-parallel-requests", "0"}, "--max-parallel-requests must be at least 1 (got 0)"},
		{[]string{"--max-parallel-requests", "-4"}, "--max-parallel-requests must be at least 1 (got -4)"},
		{[]string{"--on-duplicate", "maybe"}, `--on-duplicate must be one of error, ignore (got "maybe")`},
		{[]string{"--file-format", "xml"}, `--file-format must be one of json, jsonl, yaml, csv (got "xml")`},
	}
	p := bulkFileOf(t, 1, formatJSON)
	for _, tc := range cases {
		cmd := New(newHumanTupleCLI(t, "http://127.0.0.1:1")).writeCmd()
		cmd.SetArgs(append([]string{"--file", p}, tc.extra...))
		silenced(cmd)
		err := cmd.Execute()
		if err == nil {
			t.Errorf("%v should be rejected", tc.extra)
			continue
		}
		if got := clierr.Code(err); got != clierr.CodeUsage {
			t.Errorf("%v: exit code = %d, want %d (usage)", tc.extra, got, clierr.CodeUsage)
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%v: error = %q, want %q", tc.extra, got, tc.want)
		}
	}
}

func TestBulkDeleteOnMissingRejectsUnknownValue(t *testing.T) {
	p := bulkFileOf(t, 1, formatJSON)
	cmd := New(newHumanTupleCLI(t, "http://127.0.0.1:1")).deleteCmd()
	cmd.SetArgs([]string{"--file", p, "--force", "--on-missing", "maybe"})
	silenced(cmd)
	err := cmd.Execute()
	if err == nil || clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("--on-missing maybe should be a usage error, got %v", err)
	}
	if want := `--on-missing must be one of error, ignore (got "maybe")`; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestBulkWriteReportsProgressPerChunk(t *testing.T) {
	prevInteractive := output.Interactive
	output.Interactive = true
	t.Cleanup(func() { output.Interactive = prevInteractive })

	_, apiURL := newWriteRecorder(t)
	p := bulkFileOf(t, 3, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1"})
	_, errOut := silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wrote 1/3 tuples", "wrote 2/3 tuples", "wrote 3/3 tuples"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr should report %q, got %q", want, errOut.String())
		}
	}
}

func TestBulkWriteProgressSuppressedInMachineMode(t *testing.T) {
	prevInteractive := output.Interactive
	output.Interactive = true
	t.Cleanup(func() { output.Interactive = prevInteractive })
	prevPlain := output.Plain
	output.Plain = true
	t.Cleanup(func() { output.Plain = prevPlain })

	_, apiURL := newWriteRecorder(t)
	p := bulkFileOf(t, 3, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--max-tuples-per-write", "1"})
	_, errOut := silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "tuples") {
		t.Errorf("--plain must not emit progress, got %q", errOut.String())
	}
}

// TestBulkWritePlainSuccessIsOneRow locks the pre-overhaul --plain contract: a
// clean run prints the single count row it always has. total/complete are
// partial-failure rows and must not appear when nothing failed.
func TestBulkWritePlainSuccessIsOneRow(t *testing.T) {
	prevPlain := output.Plain
	output.Plain = true
	t.Cleanup(func() { output.Plain = prevPlain })

	_, apiURL := newWriteRecorder(t)
	p := bulkFileOf(t, 3, formatJSON)

	cmd := New(newHumanTupleCLI(t, apiURL)).writeCmd()
	cmd.SetArgs([]string{"--file", p})
	out, _ := silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "written\t3\n" {
		t.Errorf("--plain success output = %q, want %q", got, "written\t3\n")
	}
}
