package docsearch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/docsearch"
)

const twoResults = "Title: Contextual Tuples\nLink: https://openfga.dev/docs/interacting/contextual-tuples\nPage: Interacting\nContent: line one\nline two"
const second = "Title: Other\nLink: https://openfga.dev/docs/other\nPage: Other page\nContent: body"

type rpcReq struct {
	ID     *int   `json:"id"`
	Method string `json:"method"`
	Params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

// mcpServer serves the three-step handshake. toolResult is the JSON for the tools/call "result" (or full body override).
func mcpServer(t *testing.T, sse bool, sessionID string, callBody func(id int) string, sessions *[]string) *httptest.Server {
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs/mcp" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad headers: %v", r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(b, &req)
		mu.Lock()
		if sessions != nil {
			*sessions = append(*sessions, req.Method+"="+r.Header.Get("Mcp-Session-Id"))
		}
		mu.Unlock()
		var body string
		switch req.Method {
		case "initialize":
			if sessionID != "" {
				w.Header().Set("Mcp-Session-Id", sessionID)
			}
			body = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			if req.Params.Name != "search_open_fga" || req.Params.Arguments["query"] != "q" {
				t.Errorf("bad call: %+v", req)
			}
			body = callBody(*req.ID)
		}
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			// an unrelated message first, then the matching one split across data lines
			_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":999,\"result\":{}}\n\n")
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	}))
}

func textResult(id int, texts ...string) string {
	var items []string
	for _, s := range texts {
		j, _ := json.Marshal(s)
		items = append(items, fmt.Sprintf(`{"type":"text","text":%s}`, j))
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[%s]}}`, id, strings.Join(items, ","))
}

func checkTwo(t *testing.T, sse bool) {
	srv := mcpServer(t, sse, "", func(id int) string { return textResult(id, twoResults, second) }, nil)
	defer srv.Close()
	res, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results", len(res))
	}
	r := res[0]
	if r.Title != "Contextual Tuples" || r.URL != "https://openfga.dev/docs/interacting/contextual-tuples" ||
		r.MarkdownURL != r.URL+".md" || r.Page != "Interacting" || r.Snippet != "line one\nline two" {
		t.Errorf("result = %+v", r)
	}
	if res[1].MarkdownURL != "https://openfga.dev/docs/other.md" {
		t.Errorf("second = %+v", res[1])
	}
}

func TestSearchJSONResponse(t *testing.T) { checkTwo(t, false) }
func TestSearchSSEResponse(t *testing.T)  { checkTwo(t, true) }

func TestSearchEchoesSessionID(t *testing.T) {
	var seen []string
	srv := mcpServer(t, false, "sess-1", func(id int) string { return textResult(id, twoResults) }, &seen)
	defer srv.Close()
	if _, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	want := []string{"initialize=", "notifications/initialized=sess-1", "tools/call=sess-1"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("seen = %v", seen)
	}
}

// Every request after initialize carries the version the server negotiated.
func TestSearchSendsNegotiatedProtocolVersion(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		seen[req.Method] = r.Header.Get("MCP-Protocol-Version")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26"}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			_, _ = fmt.Fprint(w, textResult(*req.ID, twoResults))
		}
	}))
	defer srv.Close()
	if _, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"initialize": "", "notifications/initialized": "2025-03-26", "tools/call": "2025-03-26"}
	for m, v := range want {
		if seen[m] != v {
			t.Errorf("%s: MCP-Protocol-Version = %q, want %q", m, seen[m], v)
		}
	}
}

// sseServer answers every request as an SSE stream: write(id) runs after the
// headers are flushed, then the stream stays open until done is closed.
func sseServer(t *testing.T, write func(w http.ResponseWriter, id int)) (*httptest.Server, chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if req.Method == "initialize" {
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{}}\n\n", *req.ID)
		} else {
			write(w, *req.ID)
		}
		w.(http.Flusher).Flush()
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	return srv, done
}

// The response is returned as soon as it arrives, even though the server
// keeps the stream open and no timeout is set.
func TestSearchSSEDoesNotWaitForStreamEnd(t *testing.T) {
	srv, done := sseServer(t, func(w http.ResponseWriter, id int) {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", textResult(id, twoResults))
	})
	defer srv.Close()
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := docsearch.NewForTest(srv.URL, &http.Client{}).Search(ctx, "q")
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestSearchSSEOversizedStream(t *testing.T) {
	srv, done := sseServer(t, func(w http.ResponseWriter, _ int) {
		line := []byte("data: " + strings.Repeat("a", 1<<20) + "\n\n")
		for i := 0; i < 9; i++ {
			_, _ = w.Write(line)
		}
	})
	defer srv.Close()
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := docsearch.NewForTest(srv.URL, &http.Client{}).Search(ctx, "q")
	if err == nil || clierr.Classify(err) != clierr.KindServer || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Errorf("err = %v kind=%v", err, clierr.Classify(err))
	}
}

// markdown_url drops a link's fragment and query, and is empty for a link
// outside the docs; url keeps the link as given.
func TestSearchMarkdownURL(t *testing.T) {
	item := func(link string) string { return "Title: T\nLink: " + link + "\nContent: c" }
	srv := mcpServer(t, false, "", func(id int) string {
		return textResult(id,
			item("https://openfga.dev/docs/modeling/conditions#context"),
			item("https://openfga.dev/docs/modeling/conditions?x=1"),
			item("https://openfga.dev/blog/post"))
	}, nil)
	defer srv.Close()
	res, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err != nil || len(res) != 3 {
		t.Fatalf("res=%v err=%v", res, err)
	}
	want := []string{"https://openfga.dev/docs/modeling/conditions.md", "https://openfga.dev/docs/modeling/conditions.md", ""}
	for i, r := range res {
		if r.MarkdownURL != want[i] {
			t.Errorf("%s: markdown_url = %q, want %q", r.URL, r.MarkdownURL, want[i])
		}
	}
	if res[0].URL != "https://openfga.dev/docs/modeling/conditions#context" {
		t.Errorf("url = %q, want the link unchanged", res[0].URL)
	}
}

// A matching response whose event is never terminated before the cap is
// oversized, not a success; one terminated by the end of the stream counts.
func TestSearchSSECapBeforeUnterminatedEvent(t *testing.T) {
	srv, done := sseServer(t, func(w http.ResponseWriter, id int) {
		_, _ = fmt.Fprintf(w, "data: %s\n", textResult(id, twoResults))
		comment := []byte(":" + strings.Repeat("a", 1<<16) + "\n")
		for n := 0; n <= 8<<20; n += len(comment) {
			_, _ = w.Write(comment)
		}
	})
	defer srv.Close()
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := docsearch.NewForTest(srv.URL, &http.Client{}).Search(ctx, "q")
	if err == nil || clierr.Classify(err) != clierr.KindServer || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Errorf("err = %v kind=%v", err, clierr.Classify(err))
	}
}

func TestSearchSSEUnterminatedEventAtStreamEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *req.ID)
		if req.Method == "tools/call" {
			body = textResult(*req.ID, twoResults)
		}
		_, _ = fmt.Fprintf(w, "data: %s", body) // no trailing newline
	}))
	defer srv.Close()
	if res, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q"); err != nil || len(res) != 1 {
		t.Errorf("res=%v err=%v", res, err)
	}
}

func TestSearchUnrecognizedShape(t *testing.T) {
	srv := mcpServer(t, false, "", func(id int) string { return textResult(id, "just some prose") }, nil)
	defer srv.Close()
	_, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "format may have changed") || clierr.Classify(err) != clierr.KindError {
		t.Errorf("err = %v kind=%v", err, clierr.Classify(err))
	}
}

// A result without a content array, or with no text blocks in it, is a
// format change, not an empty search; "content": [] is a real empty result.
func TestSearchRequiresContent(t *testing.T) {
	for _, result := range []string{`{}`, `null`, `{"content":null}`,
		`{"content":[{"type":"resource_link","uri":"https://openfga.dev/docs/x","name":"x"}]}`} {
		srv := mcpServer(t, false, "", func(id int) string {
			return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
		}, nil)
		_, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "format may have changed") {
			t.Errorf("result %s: err = %v, want a format error", result, err)
		}
	}
	srv := mcpServer(t, false, "", func(id int) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[]}}`, id)
	}, nil)
	defer srv.Close()
	if res, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q"); err != nil || len(res) != 0 {
		t.Errorf("empty content: res=%v err=%v, want no results", res, err)
	}
}

func TestSearchToolError(t *testing.T) {
	srv := mcpServer(t, false, "", func(id int) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"isError":true,"content":[{"type":"text","text":"boom tool"}]}}`, id)
	}, nil)
	defer srv.Close()
	_, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "boom tool") {
		t.Errorf("err = %v", err)
	}
}

func TestSearchRPCError(t *testing.T) {
	srv := mcpServer(t, true, "", func(id int) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32000,"message":"rpc bad"}}`, id)
	}, nil)
	defer srv.Close()
	_, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "rpc bad") {
		t.Errorf("err = %v", err)
	}
}

func TestSearchNormalizesCRLF(t *testing.T) {
	srv := mcpServer(t, false, "", func(id int) string {
		return textResult(id, "Title: T\r\nLink: https://openfga.dev/docs/a\r\nContent: one\r\ntwo\rthree\r\n")
	}, nil)
	defer srv.Close()
	res, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if want := "one\ntwo\nthree"; res[0].Snippet != want {
		t.Errorf("snippet = %q, want %q", res[0].Snippet, want)
	}
	if res[0].Title != "T" || res[0].URL != "https://openfga.dev/docs/a" {
		t.Errorf("title/url = %q/%q", res[0].Title, res[0].URL)
	}
}

func TestSearchServerStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	_, err := docsearch.NewForTest(srv.URL, srv.Client()).Search(context.Background(), "q")
	if err == nil || clierr.Classify(err) != clierr.KindServer || !strings.Contains(err.Error(), "openfga.dev returned") {
		t.Errorf("err = %v kind=%v", err, clierr.Classify(err))
	}
}
