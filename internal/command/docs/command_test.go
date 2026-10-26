package docs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/docsearch"
	"github.com/sergiught/openfga-cli/internal/output"
)

const pageMD = "# Conditions\n\nBody text.\n"

// fakeDocs serves the MCP search endpoint (returning the given hits) and
// markdown pages, counting every request.
func fakeDocs(t *testing.T, hits []string, reqs *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		if r.URL.Path == "/docs/mcp" {
			b, _ := io.ReadAll(r.Body)
			var req struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(b, &req)
			w.Header().Set("Content-Type", "application/json")
			switch req.Method {
			case "initialize":
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, *req.ID)
			case "tools/call":
				var items []string
				for _, h := range hits {
					j, _ := json.Marshal(h)
					items = append(items, fmt.Sprintf(`{"type":"text","text":%s}`, j))
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[%s]}}`, *req.ID, strings.Join(items, ","))
			default:
				w.WriteHeader(http.StatusAccepted)
			}
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = io.WriteString(w, pageMD)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hit(title, slug string) string {
	return fmt.Sprintf("Title: %s\nLink: https://openfga.dev/docs/%s\nPage: %s\nContent: about %s", title, slug, title, slug)
}

var twoHits = []string{hit("Conditions", "modeling/conditions"), hit("Contextual tuples", "interacting/contextual")}

// run executes `docs <args>` against a fake server and returns stdout, stderr and the error.
func run(t *testing.T, srv *httptest.Server, asJSON bool, args ...string) (string, string, error) {
	t.Helper()
	configtest.Isolate(t)
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.JSON = asJSON
	output.Plain, output.Quiet = false, false
	g := New(c)
	g.newClient = func() *docsearch.Client { return docsearch.NewWithBaseURL(srv.URL, srv.Client()) }
	var out, errb bytes.Buffer
	g.cmd.SetOut(&out)
	g.cmd.SetErr(&errb)
	g.cmd.SetArgs(args)
	err := g.cmd.Execute()
	return out.String(), errb.String(), err
}

func TestDocsSearchTable(t *testing.T) {
	var n atomic.Int32
	out, _, err := run(t, fakeDocs(t, twoHits, &n), false, "search", "conditions")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TITLE", "URL", "Conditions", "https://openfga.dev/docs/modeling/conditions"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDocsSearchPlain(t *testing.T) {
	var n atomic.Int32
	srv := fakeDocs(t, twoHits, &n)
	configtest.Isolate(t)
	c := cli.New(log.New(io.Discard), config.New(), "test")
	output.Plain = true
	t.Cleanup(func() { output.Plain = false })
	g := New(c)
	g.newClient = func() *docsearch.Client { return docsearch.NewWithBaseURL(srv.URL, srv.Client()) }
	var out bytes.Buffer
	g.cmd.SetOut(&out)
	g.cmd.SetArgs([]string{"search", "x"})
	if err := g.cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	first := strings.Split(strings.TrimSpace(out.String()), "\n")[0]
	if first != "Conditions\thttps://openfga.dev/docs/modeling/conditions" {
		t.Errorf("plain row = %q", first)
	}
}

func TestDocsSearchJSON(t *testing.T) {
	var n atomic.Int32
	out, _, err := run(t, fakeDocs(t, twoHits, &n), true, "search", "multi", "word")
	if err != nil {
		t.Fatal(err)
	}
	var got []docsearch.Result
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
		t.Fatalf("got %d results, err=%v:\n%s", len(got), err, out)
	}
}

func TestDocsSearchMaxResults(t *testing.T) {
	var n atomic.Int32
	out, _, err := run(t, fakeDocs(t, twoHits, &n), true, "search", "x", "--max-results", "1")
	if err != nil {
		t.Fatal(err)
	}
	var got []docsearch.Result
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 {
		t.Fatalf("got %d results, err=%v", len(got), err)
	}
	if got[0].Title != "Conditions" {
		t.Errorf("kept %q, want the first hit %q", got[0].Title, "Conditions")
	}
	var m atomic.Int32
	_, _, err = run(t, fakeDocs(t, twoHits, &m), false, "search", "x", "--max-results", "0")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("--max-results 0 code = %d, err=%v", clierr.Code(err), err)
	}
	if m.Load() != 0 {
		t.Errorf("requests = %d, want 0", m.Load())
	}
}

func TestDocsSearchNoResults(t *testing.T) {
	var n atomic.Int32
	srv := fakeDocs(t, nil, &n)
	out, _, err := run(t, srv, true, "search", "zzz")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("json: out=%q err=%v", out, err)
	}
	out, errb, err := run(t, srv, false, "search", "zzz")
	if err != nil || out != "" || !strings.Contains(errb, `no results for "zzz"`) {
		t.Fatalf("human: out=%q err=%q %v", out, errb, err)
	}
}

func TestDocsSearchNeedsQuery(t *testing.T) {
	var n atomic.Int32
	_, _, err := run(t, fakeDocs(t, twoHits, &n), false, "search")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d, err=%v", clierr.Code(err), err)
	}
}

func TestDocsShowPrintsMarkdown(t *testing.T) {
	var n atomic.Int32
	out, _, err := run(t, fakeDocs(t, nil, &n), false, "show", "modeling/conditions")
	if err != nil || out != pageMD {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestDocsShowJSON(t *testing.T) {
	var n atomic.Int32
	out, _, err := run(t, fakeDocs(t, nil, &n), true, "show", "/docs/modeling/conditions")
	if err != nil {
		t.Fatal(err)
	}
	var p docsearch.Page
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.Markdown != pageMD || p.URL != "https://openfga.dev/docs/modeling/conditions" {
		t.Fatalf("page=%+v err=%v", p, err)
	}
}

func TestDocsShowRejectsOtherHost(t *testing.T) {
	var n atomic.Int32
	_, _, err := run(t, fakeDocs(t, nil, &n), false, "show", "https://evil.com/docs/x")
	if err == nil {
		t.Fatal("want error")
	}
	if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("code = %d, want %d", clierr.Code(err), clierr.CodeUsage)
	}
	if n.Load() != 0 {
		t.Errorf("server got %d requests, want 0", n.Load())
	}
}

func TestDocsWorksWithoutProfile(t *testing.T) {
	var n atomic.Int32
	// run isolates an empty config: no profile, store or API URL exists.
	if _, _, err := run(t, fakeDocs(t, twoHits, &n), false, "search", "x"); err != nil {
		t.Fatal(err)
	}
}
