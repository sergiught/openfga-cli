package docsearch_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/docsearch"
)

func TestPageFetchesMarkdown(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte("---\npreamble\n---\n# Hi\n"))
	}))
	defer srv.Close()
	c := docsearch.NewForTest(srv.URL, srv.Client())
	// The base URL only applies to the MCP endpoint and page fetch; PageURL stays canonical.
	p, err := c.Page(context.Background(), "interacting/contextual-tuples")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/docs/interacting/contextual-tuples.md" {
		t.Errorf("path = %q", gotPath)
	}
	if p.URL != "https://openfga.dev/docs/interacting/contextual-tuples" || p.Markdown != "---\npreamble\n---\n# Hi\n" {
		t.Errorf("page = %+v", p)
	}
}

func TestPageStatusAndContentType(t *testing.T) {
	for _, tc := range []struct {
		status int
		ct     string
		kind   clierr.Kind
	}{
		{404, "text/plain", clierr.KindError},
		{502, "text/plain", clierr.KindServer},
		{200, "text/html", clierr.KindError},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.ct)
			w.WriteHeader(tc.status)
		}))
		_, err := docsearch.NewForTest(srv.URL, srv.Client()).Page(context.Background(), "x/y")
		srv.Close()
		if err == nil || clierr.Classify(err) != tc.kind {
			t.Errorf("status %d ct %s: err=%v kind=%v", tc.status, tc.ct, err, clierr.Classify(err))
		}
	}
}

func TestPageURL(t *testing.T) {
	const want = "https://openfga.dev/docs/interacting/contextual-tuples"
	for _, in := range []string{
		"interacting/contextual-tuples",
		"/docs/interacting/contextual-tuples",
		"docs/interacting/contextual-tuples",
		"https://openfga.dev/docs/interacting/contextual-tuples",
		"https://openfga.dev/docs/interacting/contextual-tuples.md",
		"https://openfga.dev/docs/interacting/contextual-tuples#frag",
		"https://openfga.dev/docs/interacting/contextual-tuples?q=1",
		"https://OPENFGA.DEV/docs/interacting/contextual-tuples",
	} {
		got, err := docsearch.PageURL(in)
		if err != nil || got != want {
			t.Errorf("PageURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"http://openfga.dev/docs/x",
		"https://openfga.dev.evil.com/docs/x",
		"https://evil.com/@openfga.dev",
		"https://user@openfga.dev/docs/x",
		"//evil.com/docs/x",
		"../../etc/passwd",
		"https://openfga.dev/blog/x",
		"",
		"/docs/../blog/x",
		"https://openfga.dev/docs/%2e%2e/blog/x",
		"https://openfga.dev:8443/docs/x",
		"https://openfga.dev./docs/x",
		`docs\..\blog\x`,
		"docs/x%3Fy",
		"docs/x%23y",
	} {
		if got, err := docsearch.PageURL(in); err == nil {
			t.Errorf("PageURL(%q) = %q, want error", in, got)
		}
	}
}

func TestSearchUnreachable(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	_, err := docsearch.NewForTest("http://"+addr, http.DefaultClient).Search(context.Background(), "x")
	if err == nil {
		t.Fatal("want error")
	}
	if clierr.Code(err) != 4 {
		t.Errorf("code = %d", clierr.Code(err))
	}
	f := clierr.Friendly(err)
	if !strings.Contains(f, "openfga.dev") || strings.Contains(f, "OPENFGA_API_URL") {
		t.Errorf("friendly = %q", f)
	}
}

func TestLiveSearch(t *testing.T) {
	if os.Getenv("OFGA_LIVE_DOCS") != "1" {
		t.Skip("set OFGA_LIVE_DOCS=1 to hit openfga.dev")
	}
	res, err := docsearch.New(0).Search(context.Background(), "contextual tuples")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) < 1 {
		t.Fatal("no results")
	}
	t.Logf("%d results, first: %+v", len(res), res[0])
}

func TestPageRejectsEncodedQueryChars(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	for _, ref := range []string{"docs/x%3Fy", "docs/x%23y"} {
		if _, err := docsearch.NewForTest(srv.URL, srv.Client()).Page(context.Background(), ref); err == nil {
			t.Errorf("%s: want error", ref)
		}
	}
	if hits != 0 {
		t.Errorf("server got %d requests", hits)
	}
}

func TestRedirectOffHostIsRefused(t *testing.T) {
	other := 0
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other++ }))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	c := docsearch.NewForTest(srv.URL, &http.Client{})
	if _, err := c.Page(context.Background(), "docs/x"); err == nil {
		t.Error("Page: want error")
	}
	if _, err := c.Search(context.Background(), "q"); err == nil {
		t.Error("Search: want error")
	}
	if other != 0 {
		t.Errorf("redirect target got %d requests", other)
	}
}

func TestRedirectOnSameHostIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/x.md" {
			http.Redirect(w, r, "/docs/y.md", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	p, err := docsearch.NewForTest(srv.URL, &http.Client{}).Page(context.Background(), "docs/x")
	if err != nil || p.Markdown != "ok" {
		t.Errorf("p=%+v err=%v", p, err)
	}
}
