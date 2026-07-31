package tuple

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/output"
)

// tupleServer serves `total` tuples across pages of `pageSize`, so a test can
// exercise the auto-paging read path without a real server.
func tupleServer(t *testing.T, total, pageSize int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	sent := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		n := min(pageSize, total-sent)
		resp := openfga.ReadResponse{}
		for i := range n {
			resp.Tuples = append(resp.Tuples, openfga.Tuple{
				Key: openfga.TupleKey{
					User:     "user:anne",
					Relation: "viewer",
					Object:   "doc:" + itoa(sent+i),
				},
				Timestamp: time.Unix(1600000000, 0).UTC(),
			})
		}
		sent += n
		if sent < total {
			resp.ContinuationToken = "more"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// CLI-86: `tuples read --json` used to accumulate every tuple before emitting
// anything. Streaming must not change the bytes a script sees — the output has
// to stay the single JSON array it has always been, across page boundaries.
func TestReadJSONStreamsValidArrayAcrossPages(t *testing.T) {
	srv := tupleServer(t, 25, 10) // three pages
	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).readCmd()
	cmd.SetArgs(nil)
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	var got []openfga.Tuple
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("streamed output is not a JSON array: %v\n%s", err, out.String())
	}
	if len(got) != 25 {
		t.Fatalf("got %d tuples, want 25", len(got))
	}
	if got[0].Key.Object != "doc:0" || got[24].Key.Object != "doc:24" {
		t.Errorf("order or contents wrong: first=%q last=%q", got[0].Key.Object, got[24].Key.Object)
	}
}

// An empty result must still be a valid empty array, not nothing at all.
func TestReadJSONEmptyIsEmptyArray(t *testing.T) {
	srv := tupleServer(t, 0, 10)
	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).readCmd()
	cmd.SetArgs(nil)
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Fatalf("empty read = %q, want []", got)
	}
}

// --max-results must still stop paging early, now that the loop counts as it
// streams rather than measuring a slice.
func TestReadStreamHonorsMaxResults(t *testing.T) {
	srv := tupleServer(t, 100, 10)
	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).readCmd()
	cmd.SetArgs([]string{"--max-results", "15"})
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []openfga.Tuple
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 15 {
		t.Fatalf("got %d tuples, want 15", len(got))
	}
}

// FEAT-7 / upstream #569: --output-file frees stdout so progress and summaries
// can be shown while a long export runs.
func TestReadOutputFileWritesTuplesAndLeavesStdoutClean(t *testing.T) {
	srv := tupleServer(t, 12, 5)
	path := filepath.Join(t.TempDir(), "tuples.json")

	a := newHumanTupleCLI(t, srv.URL)
	cmd := New(a).readCmd()
	cmd.SetArgs([]string{"--output-file", path})
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "" {
		t.Errorf("stdout should be empty when writing to a file, got %q", got)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []openfga.Tuple
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("--output-file is not valid JSON: %v\n%s", err, b)
	}
	if len(got) != 12 {
		t.Fatalf("file has %d tuples, want 12", len(got))
	}

	// Relationship data, written no wider than the bulk --failed-file path.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("--output-file mode = %o, want 600", perm)
	}
}

func TestReadOutputFileRejectsDirectory(t *testing.T) {
	srv := tupleServer(t, 1, 10)
	a := newHumanTupleCLI(t, srv.URL)
	cmd := New(a).readCmd()
	cmd.SetArgs([]string{"--output-file", t.TempDir()})
	silenced(cmd)

	if err := cmd.Execute(); err == nil {
		t.Fatal("writing to a directory should fail")
	}
}

// The plain path streams too; it must keep emitting exactly one tab-separated
// record per tuple.
func TestReadPlainStreamsOneRecordPerTuple(t *testing.T) {
	defer func(p bool) { output.Plain = p }(output.Plain)
	output.Plain = true

	srv := tupleServer(t, 7, 3)
	a := newHumanTupleCLI(t, srv.URL)
	a.Plain = true
	cmd := New(a).readCmd()
	cmd.SetArgs(nil)
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("got %d records, want 7:\n%s", len(lines), out.String())
	}
	for i, ln := range lines {
		if n := strings.Count(ln, "\t"); n != 4 {
			t.Errorf("record %d has %d tabs, want 4: %q", i, n, ln)
		}
	}
}
