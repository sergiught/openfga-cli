package auth0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/auth0/go-auth0/v2/management"
	"golang.org/x/oauth2"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// readError turns an SDK failure while reading what into a CLI error. what
// names the data being read and scopes the permissions it needs. Cancellation
// is returned unwrapped so the caller exits as interrupted.
func readError(ctx context.Context, err error, what string, scopes []string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	if errors.Is(err, errKeptRateLimiting) {
		return clierr.WithKind(clierr.KindRateLimited, errKeptRateLimiting)
	}

	if errors.Is(err, context.Canceled) {
		return err
	}

	var (
		unauthorized *management.UnauthorizedError
		forbidden    *management.ForbiddenError
		rateLimited  *management.TooManyRequestsError
		retrieve     *oauth2.RetrieveError
	)

	switch {
	case errors.As(err, &retrieve):
		return clierr.WithHint("check the client id and secret, and that the application may use the Management API",
			clierr.WithKind(clierr.KindUnauthenticated,
				fmt.Errorf("Auth0 rejected the credentials: %s", tokenMessage(retrieve))))
	case errors.As(err, &unauthorized):
		return clierr.WithHint("check the client id and secret, and that the application may use the Management API",
			clierr.WithKind(clierr.KindUnauthenticated,
				fmt.Errorf("Auth0 rejected the credentials: %s", apiMessage(unauthorized.Body, unauthorized.StatusCode))))
	case errors.As(err, &forbidden):
		return clierr.WithHint("grant the application these Management API scopes: "+strings.Join(scopes, ", "),
			clierr.WithKind(clierr.KindForbidden,
				fmt.Errorf("Auth0 refused %s: %s", what, apiMessage(forbidden.Body, forbidden.StatusCode))))
	case errors.As(err, &rateLimited):
		return clierr.WithKind(clierr.KindRateLimited,
			fmt.Errorf("Auth0 is rate limiting reads of %s; try again shortly", what))
	case clierr.IsConnErr(err):
		return clierr.WithCode(clierr.CodeNetwork,
			clierr.WithKind(clierr.KindNetwork, fmt.Errorf("could not reach Auth0 reading %s: %w", what, err)))
	}

	return fmt.Errorf("reading %s: %w", what, err)
}

// apiMessage is the "message" Auth0 put in an error body, or the HTTP status
// text when there is none.
func apiMessage(body any, status int) string {
	if m, ok := body.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok && msg != "" {
			return msg
		}
	}

	return http.StatusText(status)
}

// tokenMessage is what the token endpoint said, never the request that was sent.
func tokenMessage(e *oauth2.RetrieveError) string {
	if e.ErrorDescription != "" {
		return e.ErrorDescription
	}

	if e.ErrorCode != "" {
		return e.ErrorCode
	}

	return http.StatusText(e.Response.StatusCode)
}
