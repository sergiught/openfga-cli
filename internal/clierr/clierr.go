// Package clierr centralizes how command errors map to user-facing messages
// and process exit codes, so main can stay a thin shell and every command
// surfaces failures consistently.
package clierr

import (
	"errors"
	"net"
	"strings"
)

// Process exit codes. Scripts and CI can branch on these instead of parsing
// stderr text.
const (
	CodeError      = 1 // generic runtime failure
	CodeUsage      = 2 // bad invocation (unknown flag, wrong arg count)
	CodeTestFailed = 3 // `assertions test` ran and some assertions failed
	CodeNetwork    = 4 // could not reach the OpenFGA server
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

// Code resolves the exit code for err: an explicit Coded wins, otherwise a
// network failure maps to CodeNetwork and anything else to CodeError.
func Code(err error) int {
	if err == nil {
		return 0
	}
	var c *Coded
	if errors.As(err, &c) {
		return c.C
	}
	if IsConnErr(err) {
		return CodeNetwork
	}
	return CodeError
}

// Friendly renders err for a human: connection failures get an actionable
// hint (with the original error kept for detail); everything else is returned
// as-is.
func Friendly(err error) string {
	if err == nil {
		return ""
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
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"connection refused", "no such host", "network is unreachable",
		"i/o timeout", "connection reset", "broken pipe",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
