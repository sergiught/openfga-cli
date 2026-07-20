package clierr

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/oauth2"
)

func TestCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "plain error", err: errors.New("boom"), want: CodeError},
		{name: "explicit code", err: WithCode(CodeTestFailed, errors.New("x")), want: CodeTestFailed},
		{name: "wrapped explicit code", err: fmt.Errorf("ctx: %w", WithCode(CodeUsage, errors.New("x"))), want: CodeUsage},
		{name: "connection error", err: errors.New(`Get "http://x": dial tcp: connect: connection refused`), want: CodeNetwork},
		{name: "net.Error", err: &net.OpError{Op: "dial", Err: errors.New("refused")}, want: CodeNetwork},
		{name: "local file error", err: &os.PathError{Op: "open", Path: "missing", Err: syscall.ENOENT}, want: CodeError},
		{name: "broken pipe is not a server failure", err: fmt.Errorf("write stdout: %w", syscall.EPIPE), want: CodeError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsBrokenPipe(t *testing.T) {
	if !IsBrokenPipe(fmt.Errorf("write: %w", syscall.EPIPE)) {
		t.Fatal("wrapped EPIPE should be recognized")
	}
	if IsConnErr(syscall.EPIPE) {
		t.Fatal("EPIPE should not be classified as an OpenFGA connection failure")
	}
	if IsBrokenPipe(errors.New("server returned: broken pipe")) {
		t.Fatal("an API message must not be mistaken for a local stdout pipe closure")
	}
	if !IsIgnorableBrokenPipe(fmt.Errorf("write: %w", syscall.EPIPE)) {
		t.Fatal("plain EPIPE should be ignorable")
	}
	if IsIgnorableBrokenPipe(WithCode(CodeTestFailed, syscall.EPIPE)) {
		t.Fatal("an explicit failure code must outrank EPIPE")
	}
}

func TestFriendly(t *testing.T) {
	if got := Friendly(errors.New("plain")); got != "plain" {
		t.Errorf("Friendly(plain) = %q, want passthrough", got)
	}
	conn := errors.New("dial tcp: connect: connection refused")
	got := Friendly(conn)
	if !strings.Contains(got, "cannot reach the OpenFGA server") {
		t.Errorf("Friendly(conn) missing hint: %q", got)
	}
	if !strings.Contains(got, conn.Error()) {
		t.Errorf("Friendly(conn) should keep the original detail: %q", got)
	}
}

func TestWithCodeNil(t *testing.T) {
	if WithCode(CodeError, nil) != nil {
		t.Error("WithCode(nil) should be nil")
	}
}

// connRefused mimics a dial failure at the leaf of a url.Error chain.
func connRefused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
}

func TestFriendlyTokenEndpoint(t *testing.T) {
	const api = "http://localhost:8080/stores"

	tests := []struct {
		name string
		err  error
		want string // substring the friendly headline must contain
	}{
		{
			// AUTH-11: a reachable token endpoint that rejects the credentials
			// (x/oauth2 RetrieveError) must not read as an unreachable API server.
			name: "oauth2 credential rejection",
			err: &url.Error{Op: "Get", URL: api, Err: &oauth2.RetrieveError{
				ErrorCode: "invalid_client", ErrorDescription: "bad credentials",
			}},
			want: "authentication failed at the OAuth token endpoint",
		},
		{
			// AUTH-11: the SDK's custom token-fetch surfaces a non-2xx as a string.
			name: "sdk token-endpoint non-2xx",
			err:  fmt.Errorf(`Get %q: %w`, api, errors.New("openfga: token endpoint returned 401: bad")),
			want: "authentication failed at the OAuth token endpoint",
		},
		{
			// OUT-26: token endpoint unreachable, path contains "token".
			name: "token endpoint unreachable, tokenish path",
			err: &url.Error{Op: "Get", URL: api, Err: &url.Error{
				Op: "Post", URL: "http://localhost:9999/oauth/token", Err: connRefused(),
			}},
			want: "cannot reach the OAuth token endpoint",
		},
		{
			// OUT-26: same, but the token_url path has no "token" substring — the
			// old string heuristic mis-blamed the API server here.
			name: "token endpoint unreachable, non-tokenish path",
			err: &url.Error{Op: "Get", URL: api, Err: &url.Error{
				Op: "Post", URL: "http://localhost:9999/dead", Err: connRefused(),
			}},
			want: "cannot reach the OAuth token endpoint",
		},
		{
			// A direct API dial failure (no nested token POST) is the API server.
			name: "api server unreachable",
			err:  &url.Error{Op: "Get", URL: api, Err: connRefused()},
			want: "cannot reach the OpenFGA server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Friendly(tt.err)
			if !strings.Contains(got, tt.want) {
				t.Errorf("Friendly() headline = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestSilent(t *testing.T) {
	err := Silent(CodeTestFailed)
	if !IsSilent(err) {
		t.Error("IsSilent(Silent(CodeTestFailed)) = false, want true")
	}
	if got := Code(err); got != CodeTestFailed {
		t.Errorf("Code(Silent(CodeTestFailed)) = %d, want %d", got, CodeTestFailed)
	}
	if IsSilent(errors.New("plain")) {
		t.Error("IsSilent(plain error) = true, want false")
	}
	// Wrapping preserves the silent sentinel.
	if !IsSilent(fmt.Errorf("wrapped: %w", Silent(CodeError))) {
		t.Error("IsSilent(wrapped Silent) = false, want true")
	}
}
