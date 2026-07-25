package client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/config"
)

// TestPlaintextCredentialWarning covers AUTH-2: a warning is emitted only when
// credentials would travel over cleartext http to a non-loopback host.
func TestPlaintextCredentialWarning(t *testing.T) {
	tests := []struct {
		name    string
		apiURL  string
		auth    config.Auth
		wantMsg bool
	}{
		{"http non-loopback with token", "http://api.example.com:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, true},
		{"http non-loopback client secret", "http://192.168.1.10:8080", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s"}, true},
		{"https non-loopback with token", "https://api.example.com", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http localhost", "http://localhost:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http 127.0.0.1", "http://127.0.0.1:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http ipv6 loopback", "http://[::1]:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http non-loopback no auth", "http://api.example.com", config.Auth{Method: config.AuthNone}, false},
		{"http non-loopback empty method", "http://api.example.com", config.Auth{}, false},
		// AUTH-8: api_token with no token sends no credential, so no warning.
		{"http non-loopback empty token", "http://api.example.com", config.Auth{Method: config.AuthAPIToken}, false},
		// AUTH-7: the client secret / signed assertion travels to the token
		// endpoint, so a cleartext token_url must warn even when the API is https.
		{"https api, http token_url client secret", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "http://issuer.example.com/oauth/token"}, true},
		{"https api, http token_url private key", "https://api.example.com", config.Auth{Method: config.AuthPrivateKeyJWT, KeyFile: "/k.pem", TokenURL: "http://issuer.example.com/oauth/token"}, true},
		{"https api, http token_url keyring private key", "https://api.example.com", config.Auth{Method: config.AuthPrivateKeyJWT, PrivateKey: "PEM", TokenURL: "http://issuer.example.com/oauth/token"}, true},
		{"https api and https token_url", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "https://issuer.example.com/oauth/token"}, false},
		{"http api, https token_url still warns on bearer", "http://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "https://issuer.example.com/oauth/token"}, true},
		{"client credentials no secret", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, TokenURL: "http://issuer.example.com/oauth/token"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plaintextCredentialWarning(tc.apiURL, tc.auth)
			if tc.wantMsg && got == "" {
				t.Fatalf("expected a warning, got none")
			}
			if !tc.wantMsg && got != "" {
				t.Fatalf("expected no warning, got %q", got)
			}
		})
	}
}

// TestLoadSigningKeyMethodMismatch covers AUTH-5: a key/method mismatch yields an
// actionable error instead of an opaque parse failure.
func TestLoadSigningKeyMethodMismatch(t *testing.T) {
	block := genTestRSAKeyPEM(t)

	// RSA key parsed as an EC method must fail with the actionable message.
	if _, _, err := parseSigningKey([]byte(block), "ES256"); err == nil {
		t.Fatal("expected mismatch error, got nil")
	} else if !strings.Contains(err.Error(), `does not match signing_method "ES256"`) {
		t.Fatalf("error missing actionable context: %v", err)
	}

	// RSA key with a matching method must succeed.
	if _, _, err := parseSigningKey([]byte(block), "RS256"); err != nil {
		t.Fatalf("expected RSA/RS256 to succeed, got %v", err)
	}
}

// genTestRSAKeyPEM generates a 2048-bit RSA key and returns it PEM-encoded
// (PKCS1) as a string, for use as a signing key in tests.
func genTestRSAKeyPEM(t *testing.T) string {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(rsaKey)
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return string(block)
}

// TestPrivateKeyFromInlinePEM covers loading the private_key_jwt signing key
// from the resolved config's inline PrivateKey (keyring-sourced), not a file.
func TestPrivateKeyFromInlinePEM(t *testing.T) {
	pemStr := genTestRSAKeyPEM(t)
	a := config.Auth{Method: config.AuthPrivateKeyJWT, TokenURL: "http://idp/token", ClientID: "id", PrivateKey: pemStr, SigningMethod: "RS256"}
	if _, err := authOption(a); err != nil {
		t.Fatalf("inline PEM should load: %v", err)
	}
}

// TestPrivateKeyPrefersInlineOverFile covers that PrivateKey wins over KeyFile
// when both are set, so a stale/missing key_file doesn't break a profile once
// migrated to the keyring.
func TestPrivateKeyPrefersInlineOverFile(t *testing.T) {
	pemStr := genTestRSAKeyPEM(t)
	// key_file points at a non-existent path; PrivateKey must be used instead.
	a := config.Auth{Method: config.AuthPrivateKeyJWT, TokenURL: "http://idp/token", ClientID: "id", PrivateKey: pemStr, KeyFile: "/does/not/exist", SigningMethod: "RS256"}
	if _, err := authOption(a); err != nil {
		t.Fatalf("inline PEM should take precedence over key_file: %v", err)
	}
}

// TestPrivateKeyRequiresKeyOrFile covers the error path when neither the
// inline private_key nor a key_file is configured.
func TestPrivateKeyRequiresKeyOrFile(t *testing.T) {
	a := config.Auth{Method: config.AuthPrivateKeyJWT, TokenURL: "http://idp/token", ClientID: "id"}
	if _, err := authOption(a); err == nil {
		t.Fatal("private_key_jwt with neither private_key nor key_file should error")
	}
}

func TestWithCaptureRecordsThroughTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stores":[]}`))
	}))
	defer srv.Close()

	rec := apilog.NewRecorder(8)
	c, err := New(config.Resolved{APIURL: srv.URL}, WithCapture(rec))
	if err != nil {
		t.Fatal(err)
	}
	for range c.Stores.All(context.Background(), nil) { // drive one request
	}
	if len(rec.Snapshot()) == 0 {
		t.Fatal("expected WithCapture to record at least one request")
	}
}

func TestWithTimeoutBoundsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"stores":[]}`))
	}))
	defer srv.Close()

	c, err := New(config.Resolved{APIURL: srv.URL}, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range c.Stores.All(context.Background(), nil) {
		if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("request error = %v, want timeout", err)
		}
		return
	}
	t.Fatal("expected request to time out")
}

func TestAPITokenRedirectIsRejectedBeforeCredentialForwarding(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()

	var sourceSawToken atomic.Bool
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceSawToken.Store(r.Header.Get("Authorization") == "Bearer secret")
		http.Redirect(w, r, destination.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	c, err := New(config.Resolved{
		APIURL: source.URL,
		Auth:   config.Auth{Method: config.AuthAPIToken, Token: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestErr := firstStoresError(c)
	if requestErr == nil || !strings.Contains(requestErr.Error(), "redirects are disabled") {
		t.Fatalf("request error = %v, want actionable redirect rejection", requestErr)
	}
	if !sourceSawToken.Load() {
		t.Fatal("test did not exercise an authenticated API request")
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("API token was forwarded to the redirect destination")
	}
}

func TestHTTPSDowngradeRedirectIsRejected(t *testing.T) {
	rt := &rejectRedirectTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"http://api.example/stores"}},
		}, nil
	})}
	req, err := http.NewRequest(http.MethodGet, "https://api.example/stores", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil || !strings.Contains(err.Error(), "redirects are disabled") {
		t.Fatalf("downgrade redirect error = %v", err)
	}
}

func TestOAuthTokenRedirectIsRejectedBeforeCredentialForwarding(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"forwarded","token_type":"Bearer"}`))
	}))
	defer destination.Close()

	var sourceSawCredentials atomic.Bool
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		_ = r.ParseForm()
		if (ok && user == "client" && pass == "secret") ||
			(r.Form.Get("client_id") == "client" && r.Form.Get("client_secret") == "secret") {
			sourceSawCredentials.Store(true)
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stores":[]}`))
	}))
	defer api.Close()

	c, err := New(config.Resolved{
		APIURL: api.URL,
		Auth: config.Auth{
			Method:       config.AuthClientCredentials,
			ClientID:     "client",
			ClientSecret: "secret",
			TokenURL:     source.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestErr := firstStoresError(c)
	if requestErr == nil || !strings.Contains(requestErr.Error(), "redirects are disabled") {
		t.Fatalf("request error = %v, want actionable token redirect rejection", requestErr)
	}
	if !sourceSawCredentials.Load() {
		t.Fatal("test did not exercise an authenticated OAuth token fetch")
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("OAuth credentials were forwarded to the redirect destination")
	}
}

func firstStoresError(c *openfga.Client) error {
	for _, err := range c.Stores.All(context.Background(), nil) {
		if err != nil {
			return err
		}
	}
	return nil
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// noWait is a wait function for tests: it never sleeps, so retry tests run instantly.
func noWait(context.Context, time.Duration) error { return nil }

// idempotentRetryServer counts requests to path and returns firstStatus on the
// first n calls, then 200 with an empty JSON object.
func idempotentRetryServer(t *testing.T, failStatus int, failCount int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if int(n) <= failCount {
			w.WriteHeader(failStatus)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	return srv, &attempts
}

// TestIdempotentRetryTransport_GETRetriedOn502 covers CLI-76 (a): a GET
// (always idempotent) is retried past a 502.
func TestIdempotentRetryTransport_GETRetriedOn502(t *testing.T) {
	srv, attempts := idempotentRetryServer(t, http.StatusBadGateway, 1)
	defer srv.Close()

	rt := &idempotentRetryTransport{base: http.DefaultTransport, wait: noWait}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stores", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// TestIdempotentRetryTransport_WriteNotRetriedOn502 covers CLI-76 (b): a POST
// /write is not idempotent, so a 502 must not be retried — the single 502
// response surfaces as-is.
func TestIdempotentRetryTransport_WriteNotRetriedOn502(t *testing.T) {
	srv, attempts := idempotentRetryServer(t, http.StatusBadGateway, 10)
	defer srv.Close()

	rt := &idempotentRetryTransport{base: http.DefaultTransport, wait: noWait}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/stores/01ABC/write", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (unretried)", resp.StatusCode)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on /write)", got)
	}
}

// TestIdempotentRetryTransport_CheckRetriedOn502 covers CLI-76 (d): /check is
// a POST but read-semantics, so a 502 is retried.
func TestIdempotentRetryTransport_CheckRetriedOn502(t *testing.T) {
	srv, attempts := idempotentRetryServer(t, http.StatusBadGateway, 1)
	defer srv.Close()

	rt := &idempotentRetryTransport{base: http.DefaultTransport, wait: noWait}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/stores/01ABC/check", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// TestNewRetriesWriteOn429ButNotOn502 covers CLI-76 end-to-end through New():
// 429 retries stay in effect for every RPC (via the SDK's own retry
// transport), but a 502 on POST /write is not retried.
func TestNewRetriesWriteOn429ButNotOn502(t *testing.T) {
	t.Run("429 is retried", func(t *testing.T) {
		srv, attempts := idempotentRetryServer(t, http.StatusTooManyRequests, 1)
		defer srv.Close()

		c, err := New(config.Resolved{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		if err != nil {
			t.Fatal(err)
		}
		err = c.Tuples.Write(context.Background(), &openfga.WriteRequest{
			Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{
				{User: "user:a", Relation: "reader", Object: "doc:1"},
			}},
		})
		if err != nil {
			t.Fatalf("write error = %v, want retried success", err)
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("attempts = %d, want 2 (429 retried)", got)
		}
	})

	t.Run("502 is not retried", func(t *testing.T) {
		srv, attempts := idempotentRetryServer(t, http.StatusBadGateway, 10)
		defer srv.Close()

		c, err := New(config.Resolved{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		if err != nil {
			t.Fatal(err)
		}
		err = c.Tuples.Write(context.Background(), &openfga.WriteRequest{
			Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{
				{User: "user:a", Relation: "reader", Object: "doc:1"},
			}},
		})
		if err == nil {
			t.Fatal("expected write to surface the 502 error, got nil")
		}
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1 (no retry on /write)", got)
		}
	})
}

// TestNewRetriesCheckOn502 covers CLI-76 end-to-end: /check is POST but
// read-semantics, so a 502 is retried through the full client.
func TestNewRetriesCheckOn502(t *testing.T) {
	srv, attempts := idempotentRetryServer(t, http.StatusBadGateway, 1)
	defer srv.Close()

	c, err := New(config.Resolved{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Relationships.Check(context.Background(), &openfga.CheckRequest{
		TupleKey: openfga.CheckRequestTupleKey{User: "user:a", Relation: "reader", Object: "doc:1"},
	})
	if err != nil {
		t.Fatalf("check error = %v, want retried success", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (502 retried on /check)", got)
	}
}
