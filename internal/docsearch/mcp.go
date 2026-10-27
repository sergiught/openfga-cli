package docsearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// session carries the Mcp-Session-Id the server hands out on initialize and
// the negotiated protocol version, both sent on every later request.
type session struct {
	c       *Client
	id      string
	version string
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
	if s.version != "" {
		req.Header.Set("MCP-Protocol-Version", s.version)
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
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt == "text/event-stream" {
		return matchSSE(resp.Body, *id)
	}
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if r := matchID(data, *id); r != nil {
		return r, nil
	}
	return nil, clierr.WithKind(clierr.KindError, errorString(errFormat))
}

// matchID decodes one JSON-RPC message, returning it only if it answers id.
func matchID(data []byte, id int) *rpcMessage {
	var r rpcMessage
	if json.Unmarshal(data, &r) == nil && r.ID != nil && *r.ID == id {
		return &r
	}
	return nil
}

// matchSSE reads events as they arrive and returns the response to id as
// soon as it is complete, so a server that keeps the stream open afterwards
// can't stall the request. The 8 MiB cap covers everything read.
func matchSSE(body io.Reader, id int) (*rpcMessage, error) {
	lr := &io.LimitedReader{R: body, N: maxBody + 1}
	sc := bufio.NewScanner(lr)
	sc.Buffer(make([]byte, 0, 64*1024), maxBody+1)
	var cur []string
	for {
		more := sc.Scan()
		line := strings.TrimRight(sc.Text(), "\r")
		if !more || line == "" {
			// An event ends at a blank line or at the end of the stream.
			if len(cur) > 0 {
				if r := matchID([]byte(strings.Join(cur, "\n")), id); r != nil {
					return r, nil
				}
				cur = nil
			}
			if !more {
				break
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			cur = append(cur, strings.TrimPrefix(rest, " "))
		}
	}
	if lr.N <= 0 || errors.Is(sc.Err(), bufio.ErrTooLong) {
		return nil, oversized()
	}
	if err := sc.Err(); err != nil {
		return nil, networkErr(err)
	}
	return nil, clierr.WithKind(clierr.KindError, errorString(errFormat))
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
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(init.Result, &ir)
	s.version = ir.ProtocolVersion
	if s.version == "" {
		s.version = protocolVersion
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
