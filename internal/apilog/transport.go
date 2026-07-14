package apilog

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes caps the size of each stored request/response body copy.
const maxBodyBytes = 64 << 10

// Transport returns an http.RoundTripper that records each attempt into rec.
// It sits beneath the SDK's auth/retry chain (installed via WithBaseTransport),
// so it sees the fully-decorated request and the raw response, and re-wraps both
// body streams so the SDK's own reads are never disturbed.
//
// apiURL is the resolved OpenFGA API endpoint. The SDK also routes out-of-band
// OAuth token fetches (client_credentials / private_key_jwt) through this same
// base transport, and those requests carry secrets (client_secret in the
// request body, access_token in the response body) that must never be
// captured. Only requests whose host matches apiURL's host are recorded;
// everything else passes straight through unrecorded.
func Transport(base http.RoundTripper, rec *Recorder, apiURL string) http.RoundTripper {
	var apiHost string
	if apiURL != "" {
		if u, err := url.Parse(apiURL); err == nil {
			apiHost = u.Host
		}
	}
	return &roundTripper{base: base, rec: rec, apiHost: apiHost}
}

type roundTripper struct {
	base    http.RoundTripper
	rec     *Recorder
	apiHost string

	mu      sync.Mutex
	lastURL string
	attempt int
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.apiHost == "" || req.URL.Host != rt.apiHost {
		return rt.base.RoundTrip(req) // not an OpenFGA API call — never capture (avoids leaking OAuth token-fetch bodies)
	}

	e := Entry{
		Time:       time.Now(),
		Method:     req.Method,
		URL:        req.URL.String(),
		ReqHeaders: redactHeaders(req.Header),
	}
	if req.Body != nil {
		if full, err := io.ReadAll(req.Body); err == nil {
			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(full))
			req.ContentLength = int64(len(full))
			e.ReqBody = cap64(full)
		}
	}

	start := time.Now()
	resp, err := rt.base.RoundTrip(req)
	e.Elapsed = time.Since(start)
	e.Attempt = rt.nextAttempt(e.URL)

	if err != nil {
		e.Err = err.Error()
		rt.rec.Add(e)
		return resp, err
	}

	e.Status = resp.StatusCode
	e.StatusText = resp.Status
	e.RespHeaders = resp.Header.Clone()
	e.RequestID = resp.Header.Get("Fga-Request-Id")
	e.ServerQueryDuration = resp.Header.Get("Fga-Query-Duration-Ms")

	switch {
	case strings.HasSuffix(req.URL.Path, "/streamed-list-objects"):
		e.RespBody = []byte("[streamed response not captured]")
	case resp.Body != nil:
		if full, rerr := io.ReadAll(resp.Body); rerr == nil {
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(full))
			e.RespBody = cap64(full)
		}
	}

	rt.rec.Add(e)
	return resp, err
}

// nextAttempt returns 1 for a fresh URL and increments for consecutive
// RoundTrips of the same URL, so back-to-back SDK retries roll up as attempts.
func (rt *roundTripper) nextAttempt(url string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if url == rt.lastURL {
		rt.attempt++
	} else {
		rt.lastURL = url
		rt.attempt = 1
	}
	return rt.attempt
}

// redactHeaders clones h and masks the Authorization bearer token.
func redactHeaders(h http.Header) http.Header {
	c := h.Clone()
	if c == nil {
		return http.Header{}
	}
	if _, ok := c["Authorization"]; ok {
		c.Set("Authorization", "Bearer ***redacted***")
	}
	return c
}

// cap64 returns an independent copy of b, truncated to maxBodyBytes with a
// marker when it overflows.
func cap64(b []byte) []byte {
	if len(b) <= maxBodyBytes {
		return append([]byte(nil), b...)
	}
	out := append([]byte(nil), b[:maxBodyBytes]...)
	return append(out, []byte(fmt.Sprintf("\n… [truncated, %d bytes total]", len(b)))...)
}
