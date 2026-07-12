package clierr

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code() = %d, want %d", got, tt.want)
			}
		})
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
