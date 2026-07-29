package tuple

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tuples.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

// TestBulkWriteReportsCommittedCount is the successor to the pre-overhaul
// TestWriteInBatchesReportsCommittedCount: writeInBatches is gone, but a bulk
// write whose second chunk is rejected must still report the 100 tuples that
// landed and still carry a clierr.PartialResult for the cancellation handler.
func TestBulkWriteReportsCommittedCount(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Error(w, `{"code":"validation_error","message":"stop"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	p := bulkFileOf(t, 101, formatJSON)
	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).writeCmd()
	cmd.SetArgs([]string{"--file", p})
	out, _ := silenced(cmd)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("second batch should fail")
	}
	var got struct {
		Written  int  `json:"written"`
		Total    int  `json:"total"`
		Complete bool `json:"complete"`
	}
	if jsonErr := json.Unmarshal([]byte(out.String()), &got); jsonErr != nil {
		t.Fatalf("decode %q: %v", out.String(), jsonErr)
	}
	if got.Written != 100 || got.Total != 101 || got.Complete {
		t.Fatalf("written/total/complete = %d/%d/%v, want 100/101/false", got.Written, got.Total, got.Complete)
	}
	var pr *clierr.PartialResult
	if !errors.As(err, &pr) {
		t.Fatal("a bulk write error should carry a clierr.PartialResult so a cancellation handler can report what committed")
	}
}

func TestBulkTuples(t *testing.T) {
	cmd := &cobra.Command{}

	// Bare array.
	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1"},{"user":"user:bob","relation":"editor","object":"doc:2"}]`)
	keys, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].User != "user:anne" || keys[1].Object != "doc:2" {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	// Wrapper object.
	p = writeTemp(t, `{"tuples":[{"user":"user:anne","relation":"viewer","object":"doc:1"}]}`)
	if keys, err = bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err != nil || len(keys) != 1 {
		t.Fatalf("wrapper form: keys=%v err=%v", keys, err)
	}

	// --file is mutually exclusive with positional args / field flags.
	if _, err := bulkTuples(cmd, p, formatJSON, []string{"user:anne"}, "", "", "", false); err == nil {
		t.Error("--file with positional args should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("mixed tuple inputs exit code = %d, want usage", clierr.Code(err))
	}
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "user:anne", "", "", false); err == nil {
		t.Error("--file with --user should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("mixed tuple flags exit code = %d, want usage", clierr.Code(err))
	}

	// A malformed triple is rejected.
	p = writeTemp(t, `[{"user":"anne","relation":"viewer","object":"doc:1"}]`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err == nil {
		t.Error("malformed user should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("malformed tuple exit code = %d, want usage", clierr.Code(err))
	}

	// Empty file.
	p = writeTemp(t, `[]`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err == nil {
		t.Error("empty tuple list should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("empty tuple list exit code = %d, want usage", clierr.Code(err))
	}

	// Trailing garbage after the JSON value is rejected, not silently ignored.
	p = writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1"}] garbage`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err == nil {
		t.Error("trailing garbage should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("trailing garbage exit code = %d, want usage", clierr.Code(err))
	}
}

func TestBulkTuplesCondition(t *testing.T) {
	cmd := &cobra.Command{}

	// A condition name and context land on the resulting TupleKey.
	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1","condition":{"name":"non_expired_grant","context":{"grant_duration":"10m"}}}]`)
	keys, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Condition == nil {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	if keys[0].Condition.Name != "non_expired_grant" {
		t.Errorf("condition name = %q, want non_expired_grant", keys[0].Condition.Name)
	}
	if keys[0].Condition.Context["grant_duration"] != "10m" {
		t.Errorf("condition context = %+v, want grant_duration=10m", keys[0].Condition.Context)
	}

	// An unrecognized field is a parse error naming it, rather than being
	// silently dropped.
	p = writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1","condition_ctx":{"name":"x"}}]`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err == nil {
		t.Error("unknown field should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("unknown field exit code = %d, want usage", clierr.Code(err))
	} else if !strings.Contains(err.Error(), "condition_ctx") {
		t.Errorf("error = %v, want it to name the unknown field", err)
	}

	// A condition with an empty name is rejected.
	p = writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1","condition":{"context":{"x":1}}}]`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", false); err == nil {
		t.Error("condition with empty name should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("empty condition name exit code = %d, want usage", clierr.Code(err))
	}

	// Deletes cannot carry a condition: OpenFGA deletes match by
	// user/relation/object only, so silently accepting it would mislead.
	p = writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1","condition":{"name":"non_expired_grant"}}]`)
	if _, err := bulkTuples(cmd, p, formatJSON, nil, "", "", "", true); err == nil {
		t.Error("condition on a delete input should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("condition-on-delete exit code = %d, want usage", clierr.Code(err))
	}
}

func TestNegativePaginationRejectedBeforeClientCreation(t *testing.T) {
	c := &Command{}
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{name: "read max", cmd: c.readCmd(), args: []string{"--max-results=-1"}},
		{name: "read page", cmd: c.readCmd(), args: []string{"--page-size=-1"}},
		{name: "changes max", cmd: c.changesCmd(), args: []string{"--max-results=-1"}},
		{name: "changes page", cmd: c.changesCmd(), args: []string{"--page-size=-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cmd.SetArgs(tc.args)
			err := tc.cmd.Execute()
			if got := clierr.Code(err); got != clierr.CodeUsage {
				t.Fatalf("exit code = %d, want usage; err=%v", got, err)
			}
		})
	}
}

func TestTupleFileReadFailureRemainsRuntimeError(t *testing.T) {
	_, err := bulkTuples(&cobra.Command{}, "definitely-does-not-exist.json", formatJSON, nil, "", "", "", false)
	if got := clierr.Code(err); got != clierr.CodeError {
		t.Fatalf("missing file exit code = %d, want runtime error; err=%v", got, err)
	}
}

func TestChangesPaginationIsUnboundedAndForwardsPageSize(t *testing.T) {
	var calls int
	var pageSizes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		pageSizes = append(pageSizes, r.URL.Query().Get("page_size"))
		start, count, token := 0, 75, "next"
		if r.URL.Query().Get("continuation_token") == "next" {
			start, count, token = 75, 50, ""
		}
		changes := make([]openfga.TupleChange, count)
		for i := range changes {
			changes[i] = openfga.TupleChange{
				TupleKey: openfga.TupleKey{
					User:     "user:" + strconv.Itoa(start+i),
					Relation: "viewer",
					Object:   "doc:1",
				},
				Operation: "TUPLE_OPERATION_WRITE",
				Timestamp: time.Unix(int64(start+i), 0).UTC(),
			}
		}
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{
			Changes: changes, ContinuationToken: token,
		})
	}))
	defer srv.Close()

	cmd := newChangesTestCommand(t, srv.URL)
	cmd.SetArgs([]string{"--page-size", "17"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []openfga.TupleChange
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if len(got) != 125 {
		t.Fatalf("default changes result count = %d, want all 125", len(got))
	}
	if calls != 2 {
		t.Fatalf("request count = %d, want 2 pages", calls)
	}
	for i, got := range pageSizes {
		if got != "17" {
			t.Errorf("request %d page_size = %q, want 17", i+1, got)
		}
	}
}

func TestChangesMaxResultsAndLimitAlias(t *testing.T) {
	for _, flag := range []string{"--max-results", "--limit"} {
		t.Run(flag, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.URL.Query().Get("page_size"); got != "" {
					t.Errorf("--page-size 0 sent page_size=%q, want server default", got)
				}
				changes := []openfga.TupleChange{
					{TupleKey: openfga.TupleKey{User: "user:a", Relation: "viewer", Object: "doc:1"}},
					{TupleKey: openfga.TupleKey{User: "user:b", Relation: "viewer", Object: "doc:1"}},
				}
				_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{
					Changes: changes, ContinuationToken: "next",
				})
			}))
			defer srv.Close()

			cmd := newChangesTestCommand(t, srv.URL)
			cmd.SetArgs([]string{flag, "1", "--page-size", "0"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var got []openfga.TupleChange
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || calls != 1 {
				t.Fatalf("results=%d calls=%d, want cap 1 with one request", len(got), calls)
			}
		})
	}
}

func TestTupleDryRunShorthand(t *testing.T) {
	c := &Command{}
	for _, cmd := range []*cobra.Command{c.writeCmd(), c.deleteCmd()} {
		if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
			t.Errorf("%s --dry-run shorthand = %q, want n", cmd.Name(), got)
		}
	}
}

func newChangesTestCommand(t *testing.T, apiURL string) *cobra.Command {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	a := cli.New(log.New(io.Discard), cfg, "test")
	a.JSON = true
	return New(a).changesCmd()
}

// newHumanTupleCLI builds a CLI that renders human/plain output (not JSON).
func newHumanTupleCLI(t *testing.T, apiURL string) *cli.CLI {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	return cli.New(log.New(io.Discard), cfg, "test")
}

func TestReadPlainTimestampsAreRFC3339(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{
			Tuples: []openfga.Tuple{{
				Key:       openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"},
				Timestamp: time.Unix(1600000000, 0).UTC(),
			}},
		})
	}))
	defer srv.Close()

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newHumanTupleCLI(t, srv.URL)).readCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// WRITTEN is the last (5th) tab-separated column.
	fields := strings.Split(strings.TrimSpace(out.String()), "\t")
	if len(fields) != 5 {
		t.Fatalf("row = %q, want 5 tab-separated columns", out.String())
	}
	written := fields[4]
	if _, err := time.Parse(time.RFC3339, written); err != nil {
		t.Errorf("WRITTEN = %q, not RFC3339: %v", written, err)
	}
}

func TestChangesPlainOpHasNoDecorativeGlyph(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{
			Changes: []openfga.TupleChange{
				{
					TupleKey:  openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"},
					Operation: "TUPLE_OPERATION_WRITE",
					Timestamp: time.Unix(1600000000, 0).UTC(),
				},
				{
					TupleKey:  openfga.TupleKey{User: "user:bob", Relation: "editor", Object: "doc:2"},
					Operation: "TUPLE_OPERATION_DELETE",
					Timestamp: time.Unix(1600000100, 0).UTC(),
				},
			},
		})
	}))
	defer srv.Close()

	output.Plain = true
	t.Cleanup(func() { output.Plain = false })

	cmd := New(newHumanTupleCLI(t, srv.URL)).changesCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.ContainsAny(got, "＋－") {
		t.Errorf("plain changes output leaks a decorative glyph:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", len(lines), got)
	}
	wantOps := []string{"write", "delete"}
	for i, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("row %d = %q, want 3 columns", i, line)
		}
		if _, err := time.Parse(time.RFC3339, cols[0]); err != nil {
			t.Errorf("row %d TIMESTAMP = %q, not RFC3339: %v", i, cols[0], err)
		}
		if cols[1] != wantOps[i] {
			t.Errorf("row %d OP = %q, want %q", i, cols[1], wantOps[i])
		}
	}
}

func TestChangesPrintsCountFooterOnStderr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{
			Changes: []openfga.TupleChange{
				{TupleKey: openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"}, Operation: "TUPLE_OPERATION_WRITE"},
				{TupleKey: openfga.TupleKey{User: "user:bob", Relation: "editor", Object: "doc:2"}, Operation: "TUPLE_OPERATION_DELETE"},
			},
		})
	}))
	defer srv.Close()

	cmd := New(newHumanTupleCLI(t, srv.URL)).changesCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "2 change(s)") {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), "2 change(s)")
	}
	if strings.Contains(out.String(), "change(s)") {
		t.Errorf("count footer leaked onto stdout:\n%s", out.String())
	}
}

// captureWriteRequest starts a server that decodes each request body into req
// and always answers with an empty success body.
func captureWriteRequest(t *testing.T, req *openfga.WriteRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBulkWriteFileConditionReachesRequest(t *testing.T) {
	var captured openfga.WriteRequest
	srv := captureWriteRequest(t, &captured)

	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1","condition":{"name":"non_expired_grant","context":{"grant_duration":"10m"}}}]`)

	cmd := New(newHumanTupleCLI(t, srv.URL)).writeCmd()
	cmd.SetArgs([]string{"--file", p})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Writes == nil || len(captured.Writes.TupleKeys) != 1 {
		t.Fatalf("captured request = %+v", captured)
	}
	got := captured.Writes.TupleKeys[0].Condition
	if got == nil || got.Name != "non_expired_grant" || got.Context["grant_duration"] != "10m" {
		t.Fatalf("condition = %+v, want name=non_expired_grant context.grant_duration=10m", got)
	}
}

func TestWriteSingleTupleConditionFlags(t *testing.T) {
	var captured openfga.WriteRequest
	srv := captureWriteRequest(t, &captured)

	cmd := New(newHumanTupleCLI(t, srv.URL)).writeCmd()
	cmd.SetArgs([]string{
		"user:anne", "viewer", "document:roadmap",
		"--condition", "non_expired_grant",
		"--condition-context", `{"grant_duration":"10m"}`,
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Writes == nil || len(captured.Writes.TupleKeys) != 1 {
		t.Fatalf("captured request = %+v", captured)
	}
	got := captured.Writes.TupleKeys[0].Condition
	if got == nil || got.Name != "non_expired_grant" || got.Context["grant_duration"] != "10m" {
		t.Fatalf("condition = %+v, want name=non_expired_grant context.grant_duration=10m", got)
	}
}

func TestConditionContextWithoutConditionIsUsageError(t *testing.T) {
	cmd := New(newHumanTupleCLI(t, "http://unused.invalid")).writeCmd()
	cmd.SetArgs([]string{"user:anne", "viewer", "document:roadmap", "--condition-context", `{"x":1}`})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestWriteFileWithConditionFlagIsUsageError(t *testing.T) {
	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1"}]`)
	cmd := New(newHumanTupleCLI(t, "http://unused.invalid")).writeCmd()
	cmd.SetArgs([]string{"--file", p, "--condition", "non_expired_grant"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestWriteDryRunMentionsCondition(t *testing.T) {
	cmd := New(newHumanTupleCLI(t, "http://unused.invalid")).writeCmd()
	cmd.SetArgs([]string{
		"user:anne", "viewer", "document:roadmap",
		"--condition", "non_expired_grant", "--dry-run",
	})
	var errOut bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "non_expired_grant") {
		t.Errorf("dry-run output = %q, want it to mention the condition", errOut.String())
	}
}

// TestReadConsistencyFlag pins the flag onto the wire: --consistency has no
// visible effect on the output, so only the request body proves it was sent.
func TestReadConsistencyFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"explicit value is sent", []string{"--consistency", "minimize_latency"}, `"consistency":"MINIMIZE_LATENCY"`},
		{"unset keeps the higher-consistency default", nil, `"consistency":"HIGHER_CONSISTENCY"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
			}))
			defer srv.Close()

			cmd := New(newHumanTupleCLI(t, srv.URL)).readCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, tt.want) {
				t.Fatalf("request body = %s, want %s", body, tt.want)
			}
		})
	}
}

func TestReadRejectsUnknownConsistency(t *testing.T) {
	cmd := New(newHumanTupleCLI(t, "http://127.0.0.1:1")).readCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--consistency", "eventual"})
	err := cmd.Execute()
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", got, err)
	}
}

// /read's tuple_key rule spans the three filter flags, and the server's 400 for
// it names a proto field rather than what to do about it. `tuples read` catches
// it locally, with the same rule the playground's filter form applies — the two
// surfaces used to disagree, the TUI explaining it and the CLI round-tripping.
func TestReadRejectsFiltersTheServerWouldRefuse(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"user alone", []string{"--user", "user:anne"}, "--object is required"},
		{"relation alone", []string{"--relation", "viewer"}, "--object is required"},
		{"bare type alone", []string{"--object", "document:"}, "needs --user"},
		{"object without a type", []string{"--object", "document", "--user", "user:anne"}, "must be document:roadmap"},
		{"wildcard object", []string{"--object", "document:*"}, "wildcards aren't matched"},
		{"userset object", []string{"--object", "document:1#viewer"}, "not a userset"},
		{"user without a type", []string{"--user", "anne", "--object", "document:roadmap"}, "--user"},
		{"padded object with no type", []string{"--object", "  document  ", "--user", "user:anne"}, "--object"},
		// Both flags are wrong: the user is reported first, so the order is pinned.
		{"both malformed", []string{"--user", "anne", "--object", "document"}, "--user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := New(newHumanTupleCLI(t, "http://127.0.0.1:1")).readCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("want a usage error, got nil")
			}
			if got := clierr.Code(err); got != clierr.CodeUsage {
				t.Fatalf("exit code = %d, want usage; err=%v", got, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should explain the rule (%q)", err, tc.want)
			}
			// Every other usage error in this command names a flag; these must too.
			if !strings.Contains(err.Error(), "--") {
				t.Fatalf("error %q should name the flag at fault", err)
			}
		})
	}
}

// A filter the server accepts must still reach it, and reading with no filter
// at all stays valid.
func TestReadAcceptsValidFilters(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)

	for _, args := range [][]string{
		{},
		{"--object", "document:roadmap"},
		{"--object", "document:", "--user", "user:anne"},
		{"--object", "document:roadmap", "--relation", "viewer"},
	} {
		cmd := New(newHumanTupleCLI(t, srv.URL)).readCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(bodies) != 4 {
		t.Fatalf("every valid filter should reach the server, got %d reads", len(bodies))
	}
	if _, ok := bodies[0]["tuple_key"]; ok {
		t.Fatalf("no flags must mean no tuple_key, got %v", bodies[0])
	}
	tk, _ := bodies[1]["tuple_key"].(map[string]any)
	if tk == nil || tk["object"] != "document:roadmap" {
		t.Fatalf("the filter must reach the wire, got %v", bodies[1])
	}
	tk2, _ := bodies[2]["tuple_key"].(map[string]any)
	if tk2 == nil || tk2["user"] != "user:anne" || tk2["object"] != "document:" {
		t.Fatalf("every set field must reach the wire, got %v", bodies[2])
	}
	tk3, _ := bodies[3]["tuple_key"].(map[string]any)
	if tk3 == nil || tk3["relation"] != "viewer" {
		t.Fatalf("the relation must reach the wire, got %v", bodies[3])
	}
}

// The local rule runs on trimmed values, so the values sent have to be trimmed
// too — otherwise a padded flag passes the check and then fails the server's
// own whitespace pattern, which is the round trip the check exists to prevent.
func TestReadTrimsFilterFlags(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)

	cmd := New(newHumanTupleCLI(t, srv.URL)).readCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--object", "  document:roadmap  ", "--user", " user:anne ", "--relation", "  viewer  "})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	tk, _ := body["tuple_key"].(map[string]any)
	if tk == nil || tk["object"] != "document:roadmap" || tk["user"] != "user:anne" || tk["relation"] != "viewer" {
		t.Fatalf("padded flags must reach the wire trimmed, got %v", body)
	}

	// Whitespace-only flags are no filter at all, as they are in the TUI.
	body = nil
	cmd = New(newHumanTupleCLI(t, srv.URL)).readCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--object", "   "})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["tuple_key"]; ok {
		t.Fatalf("a whitespace-only flag must read as no filter, got %v", body)
	}
}
