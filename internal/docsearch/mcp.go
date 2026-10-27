package docsearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/version"
)

const protocolVersion = "2025-06-18"

type rpcMessage struct {
	ID     *int            `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// session carries the Mcp-Session-Id the server hands out on initialize.
type session struct {
	c  *Client
	id string
}

// post sends one JSON-RPC message. For a request (id != nil) it returns the
// response whose id matches; notifications return nil.
func (s *session) post(ctx context.Context, id *int, method string, params any) (*rpcMessage, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		msg["id"] = *id
	}
	if params != nil {
		msg["params"] = params
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.c.baseURL+"/docs/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.id != "" {
		req.Header.Set("Mcp-Session-Id", s.id)
	}
	resp, err := s.c.http.Do(req)
	if err != nil {
		return nil, networkErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusErr(resp.Status, resp.StatusCode)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		s.id = sid
	}
	if id == nil {
		return nil, nil
	}
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var msgs [][]byte
	if mt == "text/event-stream" {
		msgs = sseEvents(data)
	} else {
		msgs = [][]byte{data}
	}
	for _, m := range msgs {
		var r rpcMessage
		if json.Unmarshal(m, &r) == nil && r.ID != nil && *r.ID == *id {
			return &r, nil
		}
	}
	return nil, clierr.WithKind(clierr.KindError, errorString(errFormat))
}

// sseEvents returns the data payload of each event, joining its data: lines.
func sseEvents(b []byte) [][]byte {
	var events [][]byte
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			events = append(events, []byte(strings.Join(cur, "\n")))
			cur = nil
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), maxBody+1)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			cur = append(cur, strings.TrimPrefix(rest, " "))
		}
	}
	flush()
	return events
}

// Search runs the docs search tool and returns its hits.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	s := &session{c: c}
	one, two := 1, 2
	init, err := s.post(ctx, &one, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ofga", "version": version.Resolved()},
	})
	if err != nil {
		return nil, err
	}
	if init.Error != nil {
		return nil, rpcError(init.Error.Message)
	}
	if _, err := s.post(ctx, nil, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	resp, err := s.post(ctx, &two, "tools/call", map[string]any{
		"name":      "search_open_fga",
		"arguments": map[string]any{"query": query},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, rpcError(resp.Error.Message)
	}
	var tr toolResult
	// A missing or null content leaves Content nil; "content": [] decodes to
	// an empty, non-nil slice and is a real empty result.
	if err := json.Unmarshal(resp.Result, &tr); err != nil || tr.Content == nil {
		return nil, clierr.WithKind(clierr.KindError, errorString(errFormat))
	}
	var texts []string
	for _, it := range tr.Content {
		if it.Type == "text" {
			texts = append(texts, it.Text)
		}
	}
	if tr.IsError {
		return nil, clierr.WithKind(clierr.KindError,
			fmt.Errorf("openfga.dev docs search failed: %s", strings.Join(texts, "\n")))
	}
	return parseResults(texts)
}

func rpcError(msg string) error {
	return clierr.WithKind(clierr.KindError,
		errors.New("openfga.dev docs search failed: "+msg))
}
