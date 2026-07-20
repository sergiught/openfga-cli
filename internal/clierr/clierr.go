// Package clierr centralizes how command errors map to user-facing messages
// and process exit codes, so main can stay a thin shell and every command
// surfaces failures consistently.
package clierr

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"

	"github.com/sergiught/go-openfga/openfga"
	"golang.org/x/oauth2"
)

// Process exit codes. Scripts and CI can branch on these instead of parsing
// stderr text.
const (
	CodeError      = 1   // generic runtime failure (load/parse errors, I/O, etc.)
	CodeUsage      = 2   // bad invocation (unknown flag, wrong arg count)
	CodeTestFailed = 3   // `model test` ran and some tests failed or coverage fell below --coverage-min
	CodeNetwork    = 4   // could not reach the OpenFGA server
	CodeCanceled   = 130 // interrupted (Ctrl-C); 128 + SIGINT
)

// Coded wraps an error with an explicit process exit code.
type Coded struct {
	C   int
	Err error
}

func (e *Coded) Error() string { return e.Err.Error() }
func (e *Coded) Unwrap() error { return e.Err }

// WithCode tags err with a specific exit code. Returns nil for a nil error.
func WithCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Coded{C: code, Err: err}
}

// errSilent is the sentinel carried by Silent errors; its message is never
// shown to the user (main exits early on IsSilent), so it only surfaces in
// debug logs.
var errSilent = errors.New("(handled)")

// Silent tags an exit code for a failure the command has already reported to
// the user (e.g. a model-test "N/Total test(s) failed" summary line). main
// honors the exit code but prints nothing further, so the summary is not
// duplicated. Use WithCode instead when the error carries a message the user
// still needs to see.
func Silent(code int) error {
	return &Coded{C: code, Err: errSilent}
}

// IsSilent reports whether err is a Silent error whose message was already
// surfaced to the user and should not be printed again.
func IsSilent(err error) bool {
	return errors.Is(err, errSilent)
}

// Code resolves the exit code for err: an explicit Coded wins, then an
// interruption maps to CodeCanceled, a bad invocation to CodeUsage, a network
// failure to CodeNetwork, and anything else to CodeError.
func Code(err error) int {
	if err == nil {
		return 0
	}
	var c *Coded
	if errors.As(err, &c) {
		return c.C
	}
	// Ctrl-C cancels the request context; surface the conventional 130 rather
	// than misreporting the resulting "context canceled" as a network failure.
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if IsUsageErr(err) {
		return CodeUsage
	}
	if IsConnErr(err) {
		return CodeNetwork
	}
	return CodeError
}

// IsBrokenPipe reports that the downstream stdout consumer closed its end of
// a pipe. This is normal shell control flow (for example `ofga ... | head`),
// not an OpenFGA network failure.
func IsBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

// IsIgnorableBrokenPipe reports a plain stdout EPIPE that may be treated as a
// successful short read by a downstream consumer. Explicitly coded failures
// still win even if an output write also encountered EPIPE.
func IsIgnorableBrokenPipe(err error) bool {
	if !IsBrokenPipe(err) {
		return false
	}
	var coded *Coded
	return !errors.As(err, &coded)
}

// IsUsageErr reports whether err is one of cobra's flag/argument validation
// failures, which it returns as plain errors (missing required flag, unknown
// flag/command, wrong arg count). These are bad invocations, not runtime
// failures, so they map to CodeUsage and a "--help" hint.
func IsUsageErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{
		"required flag(s)",
		"unknown flag",
		"unknown shorthand flag",
		"unknown command",
		"flag needs an argument",
		"invalid argument",
		"accepts ",  // "accepts N arg(s), received M"
		"requires ", // "requires at least N arg(s)"
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Friendly renders err for a human: connection failures get an actionable
// hint (with the original error kept for detail); everything else is returned
// as-is.
func Friendly(err error) string {
	if err == nil {
		return ""
	}
	var authErr *openfga.AuthenticationError
	if errors.As(err, &authErr) {
		return "authentication failed — check your token or credentials with `ofga profiles show`.\n" +
			"  the token may be expired, or the profile's auth method may be wrong.\n" +
			"  " + err.Error()
	}
	// A token-endpoint credential rejection reaches us wrapped inside the API
	// request's url.Error (which implements net.Error), so it would otherwise be
	// misreported as an unreachable API server. The endpoint was reached — the
	// credentials were refused — so point the fix at token_url/client_secret.
	if isTokenExchangeRejection(err) {
		return "authentication failed at the OAuth token endpoint — check client_secret, token_url, and audience.\n" +
			"  " + err.Error()
	}
	if IsConnErr(err) {
		if connToTokenEndpoint(err) {
			return "cannot reach the OAuth token endpoint. Is it running, and is token_url correct?\n" +
				"  set it with OPENFGA_TOKEN_URL or `ofga profiles set token_url <url>`\n" +
				"  " + err.Error()
		}
		return "cannot reach the OpenFGA server. Is it running, and is the API URL correct?\n" +
			"  set the URL with OPENFGA_API_URL or `ofga profiles set api_url <url>`\n" +
			"  " + err.Error()
	}
	return err.Error()
}

// isTokenExchangeRejection reports whether the OAuth token exchange itself was
// rejected by a reachable token endpoint (bad client_secret/audience/token_url),
// as opposed to the endpoint being unreachable. The SDK surfaces this as an
// x/oauth2 *RetrieveError (client_credentials) or an "openfga: token endpoint
// returned N" error, wrapped inside the API request's url.Error.
func isTokenExchangeRejection(err error) bool {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return true
	}
	return strings.Contains(err.Error(), "token endpoint returned")
}

// connToTokenEndpoint reports whether a connection failure was to an OAuth token
// endpoint rather than the API server. While serving an API call the SDK makes
// one secondary request — the token POST to token_url — so a POST url.Error
// nested below the outermost (API request) url.Error, targeting a different host,
// points the fix at token_url rather than api_url.
func connToTokenEndpoint(err error) bool {
	apiHost := ""
	seenAPI := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		ue, ok := e.(*url.Error)
		if !ok {
			continue
		}
		if !seenAPI {
			apiHost = urlHost(ue.URL) // the outermost url.Error is the API request
			seenAPI = true
			continue
		}
		if strings.EqualFold(ue.Op, "Post") {
			if h := urlHost(ue.URL); h != "" && h != apiHost {
				return true
			}
		}
	}
	return false
}

// urlHost returns the host:port of a raw URL, or "" if it can't be parsed.
func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// IsConnErr reports whether err looks like a network-level failure (refused
// connection, DNS lookup failure, timeout) rather than a normal API error. It
// checks the idiomatic net.Error interface first, then falls back to matching
// well-known substrings for errors that don't implement it.
func IsConnErr(err error) bool {
	if err == nil {
		return false
	}
	if IsBrokenPipe(err) {
		return false
	}
	// Local filesystem failures can implement net.Error through their wrapped
	// syscall on some platforms; they are runtime failures, not API transport
	// failures.
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"connection refused", "no such host", "network is unreachable",
		"i/o timeout", "connection reset",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
