package tuple

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/go-openfga/openfga"
)

// changesServer serves a changelog in pages, recording the continuation tokens
// it was sent so a test can assert on how the client resumed.
type changesServer struct {
	*httptest.Server
	mu       sync.Mutex
	gotToken []string
}

func newChangesServer(t *testing.T, total, pageSize int) *changesServer {
	t.Helper()
	cs := &changesServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("continuation_token")
		cs.mu.Lock()
		cs.gotToken = append(cs.gotToken, tok)
		cs.mu.Unlock()

		start := 0
		if tok != "" {
			start = atoiSafe(tok)
		}
		n := min(pageSize, total-start)
		resp := openfga.ReadChangesResponse{}
		for i := range n {
			resp.Changes = append(resp.Changes, openfga.TupleChange{
				TupleKey:  openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "doc:" + itoa(start+i)},
				Operation: "TUPLE_OPERATION_WRITE",
				Timestamp: time.Unix(1600000000, 0).UTC(),
			})
		}
		next := itoa(start + n)
		if start+n >= total {
			// OpenFGA signals "caught up" by handing back the same token.
			next = tok
			if next == "" {
				next = itoa(total)
			}
		}
		resp.ContinuationToken = next
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (cs *changesServer) tokens() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return append([]string(nil), cs.gotToken...)
}

// FEAT-10: --token-file records where the run stopped so the next poll resumes
// instead of replaying the whole changelog.
func TestChangesWritesResumeTokenToFile(t *testing.T) {
	srv := newChangesServer(t, 12, 5)
	path := filepath.Join(t.TempDir(), "tok")

	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).changesCmd()
	cmd.SetArgs([]string{"--token-file", path})
	silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("--token-file not written: %v", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		t.Fatal("--token-file is empty; a poller has nothing to resume from")
	}
	if perm := mustStat(t, path).Mode().Perm(); perm != 0o600 {
		t.Errorf("--token-file mode = %o, want 600", perm)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// --continuation-token must be sent on the first request, not silently dropped;
// otherwise a "resume" quietly re-reads the changelog from the start.
func TestChangesSendsSuppliedContinuationToken(t *testing.T) {
	srv := newChangesServer(t, 12, 5)

	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).changesCmd()
	cmd.SetArgs([]string{"--continuation-token", "5"})
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	toks := srv.tokens()
	if len(toks) == 0 || toks[0] != "5" {
		t.Fatalf("first request sent token %q, want 5", firstOr(toks, "<none>"))
	}

	var got []openfga.TupleChange
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out.String())
	}
	// Resuming at 5 of 12 leaves 7.
	if len(got) != 7 {
		t.Fatalf("got %d changes, want 7", len(got))
	}
	if got[0].TupleKey.Object != "doc:5" {
		t.Errorf("resumed at %q, want doc:5", got[0].TupleKey.Object)
	}
}

func firstOr(s []string, fallback string) string {
	if len(s) == 0 {
		return fallback
	}
	return s[0]
}

// Stopping mid-page must report the token that produced the page, not the one
// past it — resuming from the latter would skip the changes that were fetched
// but never printed.
func TestChangesMaxResultsResumeTokenDoesNotSkip(t *testing.T) {
	srv := newChangesServer(t, 20, 10)
	path := filepath.Join(t.TempDir(), "tok")

	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).changesCmd()
	cmd.SetArgs([]string{"--max-results", "3", "--token-file", path})
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []openfga.TupleChange
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d changes, want 3", len(got))
	}
	// The first page was fetched with the empty token, so that is what a
	// correct resume replays from; "10" would skip changes 3..9 entirely.
	if tok := strings.TrimSpace(readFile(t, path)); tok != "" {
		t.Errorf("resume token = %q, want the token that produced the partial page (empty)", tok)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The JSON shape must stay the bare array it has always been: wrapping it to
// carry the token would break every `jq '.[]'` in the wild.
func TestChangesJSONRemainsBareArray(t *testing.T) {
	srv := newChangesServer(t, 4, 10)
	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).changesCmd()
	cmd.SetArgs(nil)
	out, _ := silenced(cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); !strings.HasPrefix(got, "[") {
		t.Fatalf("changes --json = %q, want a bare array", got)
	}
}

// Guard the URL-encoding assumption the server helper relies on.
func TestChangesTokenIsSentAsQueryParam(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{})
	}))
	defer srv.Close()

	a := newHumanTupleCLI(t, srv.URL)
	a.JSON = true
	cmd := New(a).changesCmd()
	cmd.SetArgs([]string{"--continuation-token", "abc/def+ghi"})
	silenced(cmd)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.Get("continuation_token") != "abc/def+ghi" {
		t.Errorf("continuation_token = %q, want it round-tripped intact", got.Get("continuation_token"))
	}
}
