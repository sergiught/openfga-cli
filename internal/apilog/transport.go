package apilog

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes caps the size of each stored request/response body copy.
const maxBodyBytes = 64 << 10

// sensitiveBodyFields lists field/parameter names whose values must never be
// stored in a captured body, even for requests to the configured API host.
// This is defense-in-depth on top of the host check in RoundTrip: the primary
// control is that OAuth token-fetch traffic (a different host, normally the
// IdP) is never captured at all, but these names are redacted unconditionally
// in case a deployment ever fronts both the API and the token endpoint behind
// the same host (e.g. a gateway), or a future field carries a secret.
var sensitiveBodyFields = []string{
	"client_secret", "access_token", "refresh_token", "id_token",
	"client_assertion", "assertion", "private_key",
}

// redactJSONBody masks the value of any sensitive field in body when body
// looks like `"field": "value"` JSON (quoted string values only — the fields
// above are never numbers/booleans/objects in practice).
var redactJSONBody = func() func([]byte) []byte {
	field := strings.Join(sensitiveBodyFields, "|")
	complete := regexp.MustCompile(`(?i)"(` + field + `)"\s*:\s*"(?:\\.|[^"\\])*"`)
	truncated := regexp.MustCompile(`(?i)"(` + field + `)"\s*:\s*"(?:\\.|[^"\\])*$`)
	return func(b []byte) []byte {
		b = complete.ReplaceAll(b, []byte(`"$1":"******"`))
		return truncated.ReplaceAll(b, []byte(`"$1":"******"`))
	}
}()

// redactFormBody masks the value of any sensitive field in body when body
// looks like `application/x-www-form-urlencoded` (field=value&field=value),
// the encoding OAuth token requests commonly use.
var redactFormBody = func() func([]byte) []byte {
	pattern := `(?i)(^|&)(` + strings.Join(sensitiveBodyFields, "|") + `)=[^&]*`
	re := regexp.MustCompile(pattern)
	return func(b []byte) []byte {
		return re.ReplaceAll(b, []byte(`$1$2=******`))
	}
}()

// redactBody masks sensitive field values in a captured request/response body,
// trying both the JSON and form-encoded shapes since either can appear
// regardless of Content-Type.
func redactBody(b []byte) []byte {
	return redactFormBody(redactJSONBody(b))
}

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
// everything else passes straight through unrecorded. As defense-in-depth for
// deployments where the token endpoint shares a host with the API (e.g. behind
// a gateway), captured bodies also go through redactBody, which masks known
// secret field names regardless of host.
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
		URL:        redactURL(req.URL),
		ReqHeaders: redactHeaders(req.Header),
	}
	var reqCapture *bodyCapture
	if req.Body != nil {
		reqCapture = newBodyCapture()
		req.Body = &capturingReadCloser{ReadCloser: req.Body, capture: reqCapture}
	}

	start := time.Now()
	resp, err := rt.base.RoundTrip(req)
	e.Elapsed = time.Since(start)
	e.Attempt = rt.nextAttempt(e.URL)
	if reqCapture != nil {
		e.ReqBody = reqCapture.body()
	}

	if err != nil {
		e.Err = err.Error()
		rt.rec.Add(e)
		return resp, err
	}

	e.Status = resp.StatusCode
	e.StatusText = resp.Status
	e.RespHeaders = redactHeaders(resp.Header)
	e.RequestID = resp.Header.Get("Fga-Request-Id")
	e.ServerQueryDuration = resp.Header.Get("Fga-Query-Duration-Ms")

	switch {
	case strings.HasSuffix(req.URL.Path, "/streamed-list-objects"):
		e.RespBody = []byte("[streamed response not captured]")
		rt.rec.Add(e)
	case resp.Body == nil:
		rt.rec.Add(e)
	default:
		capture := newBodyCapture()
		resp.Body = &recordingBody{
			ReadCloser: resp.Body,
			capture:    capture,
			finish: func() {
				e.RespBody = capture.body()
				rt.rec.Add(e)
			},
		}
	}

	return resp, err
}

type bodyCapture struct {
	data  []byte
	total int64
}

func newBodyCapture() *bodyCapture {
	return &bodyCapture{data: make([]byte, 0, maxBodyBytes)}
}

func (c *bodyCapture) add(p []byte) {
	c.total += int64(len(p))
	remaining := maxBodyBytes - len(c.data)
	if remaining > 0 {
		c.data = append(c.data, p[:min(remaining, len(p))]...)
	}
}

func (c *bodyCapture) body() []byte {
	out := redactBody(append([]byte(nil), c.data...))
	if c.total > maxBodyBytes {
		out = append(out, []byte(fmt.Sprintf("\n… [truncated, %d bytes total]", c.total))...)
	}
	return out
}

type capturingReadCloser struct {
	io.ReadCloser
	capture *bodyCapture
}

func (r *capturingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.capture.add(p[:n])
	return n, err
}

type recordingBody struct {
	io.ReadCloser
	capture *bodyCapture
	finish  func()
	once    sync.Once
}

func (r *recordingBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.capture.add(p[:n])
	if err != nil {
		r.once.Do(r.finish)
	}
	return n, err
}

func (r *recordingBody) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.finish)
	return err
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

// redactHeaders clones h and masks credentials and session material commonly
// carried in request or response headers.
func redactHeaders(h http.Header) http.Header {
	c := h.Clone()
	if c == nil {
		return http.Header{}
	}

	if _, ok := c["Authorization"]; ok {
		c.Set("Authorization", "Bearer ***redacted***")
	}
	for _, name := range []string{
		"Proxy-Authorization",
		"Cookie",
		"Set-Cookie",
		"X-API-Key",
		"X-Auth-Token",
	} {
		if c.Values(name) != nil {
			c.Set(name, "******")
		}
	}
	return c
}

func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	copy := *u
	query := copy.Query()
	for _, key := range sensitiveBodyFields {
		if query.Has(key) {
			query.Set(key, "******")
		}
	}
	for _, key := range []string{"token", "api_key"} {
		if query.Has(key) {
			query.Set(key, "******")
		}
	}
	copy.RawQuery = query.Encode()
	return copy.String()
}
