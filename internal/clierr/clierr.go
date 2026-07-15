// Package clierr centralizes how command errors map to user-facing messages
// and process exit codes, so main can stay a thin shell and every command
// surfaces failures consistently.
package clierr

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/sergiught/go-openfga/openfga"
)

// Process exit codes. Scripts and CI can branch on these instead of parsing
// stderr text.
const (
	CodeError      = 1   // generic runtime failure
	CodeUsage      = 2   // bad invocation (unknown flag, wrong arg count)
	CodeTestFailed = 3   // `assertions test` ran and some assertions failed
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
	if IsConnErr(err) {
		return "cannot reach the OpenFGA server. Is it running, and is the API URL correct?\n" +
			"  set the URL with OPENFGA_API_URL or `ofga profiles set api_url <url>`\n" +
			"  " + err.Error()
	}
	return err.Error()
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
