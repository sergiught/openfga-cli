package clierr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
)

// Kind is a stable, machine-readable error category. Agents branch on it
// instead of parsing messages.
type Kind string

// Error kinds carried in the JSON error envelope's "code" field.
const (
	KindUsage                Kind = "usage"
	KindConfirmationRequired Kind = "confirmation_required"
	KindValidation           Kind = "validation"
	KindAlreadyExists        Kind = "already_exists"
	KindUnauthenticated      Kind = "unauthenticated"
	KindForbidden            Kind = "forbidden"
	KindNotFound             Kind = "not_found"
	KindRateLimited          Kind = "rate_limited"
	KindServer               Kind = "server"
	KindNetwork              Kind = "network"
	KindTestFailed           Kind = "test_failed"
	KindPartialFailure       Kind = "partial_failure"
	KindCanceled             Kind = "canceled"
	KindError                Kind = "error"
)

// Kinds lists every Kind, in the order the error schema publishes them.
func Kinds() []Kind {
	return []Kind{
		KindUsage, KindConfirmationRequired, KindValidation, KindAlreadyExists,
		KindUnauthenticated, KindForbidden, KindNotFound, KindRateLimited,
		KindServer, KindNetwork, KindTestFailed, KindPartialFailure,
		KindCanceled, KindError,
	}
}

type kindError struct {
	kind Kind
	err  error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }

type hintError struct {
	hint string
	err  error
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }

type detailsError struct {
	details []map[string]any
	err     error
}

func (e *detailsError) Error() string { return e.err.Error() }
func (e *detailsError) Unwrap() error { return e.err }

// WithKind tags err with an explicit Kind. Returns nil for a nil error; the
// outermost WithKind wins.
func WithKind(k Kind, err error) error {
	if err == nil {
		return nil
	}
	return &kindError{kind: k, err: err}
}

// HasKind reports whether err carries an explicit Kind set with WithKind.
func HasKind(err error) bool {
	var ke *kindError
	return errors.As(err, &ke)
}

// WithHint attaches a remediation hint to err. Returns nil for a nil error;
// the outermost WithHint wins.
func WithHint(hint string, err error) error {
	if err == nil {
		return nil
	}
	return &hintError{hint: hint, err: err}
}

// WithDetails appends structured detail objects to err. Returns nil for a nil
// error.
func WithDetails(err error, details ...map[string]any) error {
	if err == nil {
		return nil
	}
	return &detailsError{details: details, err: err}
}

// Classify maps err to its stable Kind.
func Classify(err error) Kind {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return KindCanceled
	}
	var ke *kindError
	if errors.As(err, &ke) {
		return ke.kind
	}
	var coded *Coded
	if errors.As(err, &coded) {
		switch coded.C {
		case CodeUsage:
			return KindUsage
		case CodeTestFailed:
			return KindTestFailed
		case CodeCanceled:
			return KindCanceled
		case CodeNetwork:
			return KindNetwork
		}
	}
	if IsUsageErr(err) {
		return KindUsage
	}
	if isTokenExchangeRejection(err) {
		return KindUnauthenticated
	}
	if IsConnErr(err) {
		return KindNetwork
	}
	var resp *openfga.ErrorResponse
	if !errors.As(err, &resp) || resp == nil {
		var a *openfga.AuthenticationError
		if errors.As(err, &a) {
			return KindUnauthenticated
		}
		return KindError
	}
	// The SDK reports both 401 and 403 as AuthenticationError, so the status
	// must be checked before the type.
	var authErr *openfga.AuthenticationError
	if errors.As(err, &authErr) && resp.StatusCode() != 403 {
		return KindUnauthenticated
	}
	var (
		notFound *openfga.NotFoundError
		rate     *openfga.RateLimitError
		internal *openfga.InternalError
		valid    *openfga.ValidationError
	)
	status := resp.StatusCode()
	switch {
	case status == 401:
		return KindUnauthenticated
	case status == 403:
		return KindForbidden
	case errors.As(err, &notFound) || status == 404:
		return KindNotFound
	case errors.As(err, &rate) || status == 429:
		return KindRateLimited
	case errors.As(err, &internal) || status >= 500:
		return KindServer
	case errors.As(err, &valid) || status == 400:
		// OpenFGA gives a duplicate write no code of its own: it answers with
		// the generic write_failed_due_to_invalid_input and only the message
		// says "already exists". This string check is unavoidable.
		if resp.Code == "write_failed_due_to_invalid_input" && strings.Contains(resp.Message, "already exists") {
			return KindAlreadyExists
		}
		return KindValidation
	}
	return KindError
}

// Envelope is the JSON document written to stderr for a failed command under
// machine output.
type Envelope struct {
	Error EnvelopeBody `json:"error"`
}

// EnvelopeBody is the payload of an Envelope.
type EnvelopeBody struct {
	Code    Kind             `json:"code"`
	Message string           `json:"message"`
	Status  int              `json:"status,omitempty"`
	APICode string           `json:"api_code,omitempty"`
	Hint    string           `json:"hint,omitempty"`
	Details []map[string]any `json:"details"`
}

// NewEnvelope builds the machine-readable form of err.
func NewEnvelope(err error) Envelope {
	body := EnvelopeBody{Code: Classify(err), Details: []map[string]any{}}
	headline, friendlyHint, cause := friendlyParts(err)
	if s := SilentSummary(err); s != "" {
		body.Message = s
	} else {
		body.Message, _, _ = strings.Cut(headline, "\n")
	}

	var resp *openfga.ErrorResponse
	if errors.As(err, &resp) && resp != nil {
		body.Status = resp.StatusCode()
		body.APICode = resp.Code
	}

	body.Hint = friendlyHint
	var he *hintError
	if errors.As(err, &he) {
		body.Hint = he.hint
	}

	// Walk the chain so every WithDetails layer contributes, outermost first.
	for e := err; e != nil; e = errors.Unwrap(e) {
		if de, ok := e.(*detailsError); ok {
			body.Details = append(body.Details, de.details...)
		}
	}
	if cause != "" {
		body.Details = append(body.Details, map[string]any{"cause": cause})
	}
	if body.Code == KindUsage {
		for _, s := range cobraSuggestions(err.Error()) {
			body.Details = append(body.Details, map[string]any{"suggestion": s})
		}
	}
	return Envelope{Error: body}
}

// cobraSuggestions extracts the entries under cobra's "Did you mean this?"
// block of an unknown-command message.
func cobraSuggestions(msg string) []string {
	_, rest, ok := strings.Cut(msg, "Did you mean this?")
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// WriteLine writes the envelope as one compact JSON line.
func (e Envelope) WriteLine(w io.Writer) error {
	if e.Error.Details == nil {
		e.Error.Details = []map[string]any{}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
