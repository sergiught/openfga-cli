package tuple

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestWriteInBatchesReportsCommittedCount(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Error(w, `{"code":"validation_error","message":"stop"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	cl, err := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]openfga.TupleKey, 101)
	for i := range keys {
		keys[i] = openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"}
	}
	completed, err := writeInBatches(context.Background(), cl, keys, false)
	if err == nil {
		t.Fatal("second batch should fail")
	}
	if completed != 100 {
		t.Fatalf("completed = %d, want 100", completed)
	}
}

func TestBulkTuples(t *testing.T) {
	cmd := &cobra.Command{}

	// Bare array.
	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1"},{"user":"user:bob","relation":"editor","object":"doc:2"}]`)
	keys, err := bulkTuples(cmd, p, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].User != "user:anne" || keys[1].Object != "doc:2" {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	// Wrapper object.
	p = writeTemp(t, `{"tuples":[{"user":"user:anne","relation":"viewer","object":"doc:1"}]}`)
	if keys, err = bulkTuples(cmd, p, nil, "", "", ""); err != nil || len(keys) != 1 {
		t.Fatalf("wrapper form: keys=%v err=%v", keys, err)
	}

	// --file is mutually exclusive with positional args / field flags.
	if _, err := bulkTuples(cmd, p, []string{"user:anne"}, "", "", ""); err == nil {
		t.Error("--file with positional args should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("mixed tuple inputs exit code = %d, want usage", clierr.Code(err))
	}
	if _, err := bulkTuples(cmd, p, nil, "user:anne", "", ""); err == nil {
		t.Error("--file with --user should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("mixed tuple flags exit code = %d, want usage", clierr.Code(err))
	}

	// A malformed triple is rejected.
	p = writeTemp(t, `[{"user":"anne","relation":"viewer","object":"doc:1"}]`)
	if _, err := bulkTuples(cmd, p, nil, "", "", ""); err == nil {
		t.Error("malformed user should be rejected")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("malformed tuple exit code = %d, want usage", clierr.Code(err))
	}

	// Empty file.
	p = writeTemp(t, `[]`)
	if _, err := bulkTuples(cmd, p, nil, "", "", ""); err == nil {
		t.Error("empty tuple list should error")
	} else if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("empty tuple list exit code = %d, want usage", clierr.Code(err))
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
	_, err := bulkTuples(&cobra.Command{}, "definitely-does-not-exist.json", nil, "", "", "")
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
