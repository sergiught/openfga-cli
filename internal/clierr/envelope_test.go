package clierr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/pflag"
)

func TestClassifySDKResponses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    Kind
		apiCode string
	}{
		{"validation", 400, `{"code":"validation_error","message":"bad"}`, KindValidation, "validation_error"},
		{"already exists", 400, `{"code":"write_failed_due_to_invalid_input","message":"cannot write a tuple which already exists"}`, KindAlreadyExists, "write_failed_due_to_invalid_input"},
		{"unauthenticated", 401, `{"code":"unauthenticated","message":"no"}`, KindUnauthenticated, "unauthenticated"},
		{"forbidden", 403, `{"code":"forbidden","message":"no"}`, KindForbidden, "forbidden"},
		{"not found", 404, `{"code":"store_id_not_found","message":"gone"}`, KindNotFound, "store_id_not_found"},
		{"rate limited", 429, `{"code":"rate","message":"slow"}`, KindRateLimited, "rate"},
		{"server", 500, `{"code":"internal_error","message":"oops"}`, KindServer, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c, err := openfga.NewClient(srv.URL, openfga.WithoutRetry())
			if err != nil {
				t.Fatal(err)
			}
			var callErr error
			for _, e := range c.Stores.All(context.Background(), nil) {
				if e != nil {
					callErr = e
					break
				}
			}
			if callErr == nil {
				t.Fatal("expected an error")
			}
			if got := Classify(callErr); got != tt.want {
				t.Errorf("Classify = %q, want %q (%v)", got, tt.want, callErr)
			}
			env := NewEnvelope(fmt.Errorf("wrapped: %w", callErr)).Error
			if env.Code != tt.want || env.Status != tt.status || env.APICode != tt.apiCode {
				t.Errorf("envelope = %+v, want code %q status %d api_code %q", env, tt.want, tt.status, tt.apiCode)
			}
		})
	}
}

func TestClassifyLocalErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	_, dialErr := net.Dial("tcp", addr)
	if dialErr == nil {
		t.Skip("port unexpectedly open")
	}

	t.Run("pflag", func(t *testing.T) {
		if got := NewEnvelope(&pflag.NotExistError{}).Error.Code; got != KindUsage {
			t.Errorf("code = %q, want usage", got)
		}
	})
	t.Run("unknown command", func(t *testing.T) {
		e := NewEnvelope(errors.New("unknown command \"lisst\" for \"ofga stores\"\n\nDid you mean this?\n\tlist\n")).Error
		if e.Code != KindUsage {
			t.Errorf("code = %q, want usage", e.Code)
		}
		if len(e.Details) != 1 || e.Details[0]["suggestion"] != "list" {
			t.Errorf("details = %v, want one suggestion list", e.Details)
		}
	})
	t.Run("network", func(t *testing.T) {
		e := NewEnvelope(dialErr).Error
		if e.Code != KindNetwork {
			t.Errorf("code = %q, want network", e.Code)
		}
		if len(e.Details) == 0 || e.Details[0]["cause"] == "" || e.Details[0]["cause"] == nil {
			t.Errorf("details = %v, want cause", e.Details)
		}
		if !strings.Contains(e.Hint, "OPENFGA_API_URL") {
			t.Errorf("hint = %q", e.Hint)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		if got := NewEnvelope(context.Canceled).Error.Code; got != KindCanceled {
			t.Errorf("code = %q", got)
		}
	})
	t.Run("silent test failed", func(t *testing.T) {
		e := NewEnvelope(Silent(CodeTestFailed, "1/2 test(s) failed")).Error
		if e.Code != KindTestFailed || e.Message != "1/2 test(s) failed" {
			t.Errorf("envelope = %+v", e)
		}
	})
	t.Run("partial failure", func(t *testing.T) {
		e := NewEnvelope(WithKind(KindPartialFailure, Silent(CodeError, "wrote 2, failed 1 of 3 tuple(s)"))).Error
		if e.Code != KindPartialFailure {
			t.Errorf("code = %q", e.Code)
		}
	})
	t.Run("plain", func(t *testing.T) {
		e := NewEnvelope(errors.New("boom")).Error
		if e.Code != KindError || e.Message != "boom" {
			t.Errorf("envelope = %+v", e)
		}
	})
	t.Run("hint and details wrappers", func(t *testing.T) {
		err := WithDetails(WithHint("try x", WithKind(KindValidation, errors.New("bad"))), map[string]any{"a": 1})
		e := NewEnvelope(err).Error
		if e.Code != KindValidation || e.Hint != "try x" || len(e.Details) != 1 {
			t.Errorf("envelope = %+v", e)
		}
		if WithKind(KindError, nil) != nil || WithHint("h", nil) != nil || WithDetails(nil) != nil {
			t.Error("wrappers must be nil-safe")
		}
	})
}

func TestEnvelopeWriteLine(t *testing.T) {
	var buf bytes.Buffer
	if err := NewEnvelope(errors.New("a\nb")).WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	want := `{"error":{"code":"error","message":"a","details":[]}}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
