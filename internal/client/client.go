// Package client constructs a configured go-openfga client from resolved config.
package client

import (
	"crypto"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
	"github.com/sergiught/openfga-cli/internal/config"
)

// responseHeaderTimeout bounds how long the client waits for a server to start
// responding after the request is sent, so a server that accepts the connection
// but never replies fails fast instead of hanging indefinitely.
const responseHeaderTimeout = 30 * time.Second

// baseTransport returns the network-level transport placed beneath the SDK's
// auth/retry chain. It clones the standard transport (keeping proxy, dial, and
// TLS defaults) and adds a response-header timeout.
func baseTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return t
}

// Option configures optional client behavior layered on top of the resolved
// config (e.g. request capture for the TUI).
type Option func(*options)

type options struct {
	capture *apilog.Recorder
}

// WithCapture records every HTTP request/response into rec by wrapping the base
// transport. Used by the playground's API Logs view; unused by CLI commands.
func WithCapture(rec *apilog.Recorder) Option {
	return func(o *options) { o.capture = rec }
}

// New builds an *openfga.Client from a resolved configuration. The store and
// authorization-model IDs are registered as client defaults so per-call
// overrides remain optional.
func New(r config.Resolved, opts ...Option) (*openfga.Client, error) {
	if r.APIURL == "" {
		return nil, fmt.Errorf("no API URL configured: set one with --api-url, OPENFGA_API_URL, or `ofga profiles set`")
	}

	var o options
	for _, fn := range opts {
		fn(&o)
	}

	base := baseTransport()
	if o.capture != nil {
		base = apilog.Transport(base, o.capture)
	}

	opts2 := []openfga.Option{
		openfga.WithUserAgent("ofga-cli"),
		openfga.WithDefaultConsistency(openfga.ConsistencyHigherConsistency),
		openfga.WithBaseTransport(base),
		// Retry transient server errors, not just 429 (the SDK default). A
		// partial RetryConfig keeps the SDK's attempt/backoff defaults.
		openfga.WithRetry(openfga.RetryConfig{RetryableStatus: []int{429, 500, 502, 503, 504}}),
	}
	if r.StoreID != "" {
		opts2 = append(opts2, openfga.WithStoreID(r.StoreID))
	}
	if r.ModelID != "" {
		opts2 = append(opts2, openfga.WithAuthorizationModelID(r.ModelID))
	}
	authOpt, err := authOption(r.Auth)
	if err != nil {
		return nil, err
	}
	if authOpt != nil {
		opts2 = append(opts2, authOpt)
	}
	if msg := plaintextCredentialWarning(r.APIURL, r.Auth); msg != "" {
		httpWarnOnce.Do(func() { fmt.Fprintln(os.Stderr, msg) })
	}

	c, err := openfga.NewClient(r.APIURL, opts2...)
	if err != nil {
		// The SDK's errors are already user-facing (e.g. `invalid store ID …`);
		// don't double-wrap them with an internal-sounding prefix.
		return nil, err
	}
	return c, nil
}

// httpWarnOnce guards the plaintext-credentials warning so it prints at most
// once per process even if New is called repeatedly.
var httpWarnOnce sync.Once

// plaintextCredentialWarning returns a warning message when a credential would
// be sent over cleartext http to a non-loopback host, or "" when the connection
// is safe. It checks the API endpoint (which receives the bearer token) and, for
// OAuth methods, the token endpoint — which receives the far more sensitive,
// longer-lived client secret / signed assertion and is otherwise unchecked.
func plaintextCredentialWarning(apiURL string, a config.Auth) string {
	switch a.Method {
	case config.AuthAPIToken:
		// No credential is actually sent for an empty token, so don't warn.
		if a.Token == "" {
			return ""
		}
		if host := cleartextHost(apiURL); host != "" {
			return credentialWarning(host)
		}
	case config.AuthClientCredentials, config.AuthPrivateKeyJWT:
		hasCred := (a.Method == config.AuthClientCredentials && a.ClientSecret != "") ||
			(a.Method == config.AuthPrivateKeyJWT && a.KeyFile != "")
		if hasCred {
			if host := cleartextHost(a.TokenURL); host != "" {
				return credentialWarning(host + " (token endpoint)")
			}
		}
		if host := cleartextHost(apiURL); host != "" {
			return credentialWarning(host)
		}
	}
	return ""
}

// cleartextHost returns the host of rawurl when a credential sent to it would
// travel over plaintext http to a non-loopback address, or "" when the endpoint
// is safe (https, empty, or a loopback/localhost host).
func cleartextHost(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ""
	}
	return host
}

func credentialWarning(host string) string {
	return "warning: sending credentials over plaintext http to " + host + "; use https or a loopback address"
}

// authOption maps the profile's auth config to the matching client option, or
// nil when the profile is unauthenticated. It errors on an unknown method or an
// unreadable / unparsable signing key.
func authOption(a config.Auth) (openfga.Option, error) {
	switch a.Method {
	case "", config.AuthNone:
		return nil, nil
	case config.AuthAPIToken:
		if a.Token == "" {
			return nil, nil
		}
		return openfga.WithAPIToken(a.Token), nil
	case config.AuthClientCredentials:
		return openfga.WithClientCredentials(openfga.ClientCredentialsConfig{
			TokenURL:     a.TokenURL,
			ClientID:     a.ClientID,
			ClientSecret: a.ClientSecret,
			Audience:     a.Audience,
			Scopes:       a.Scopes,
		}), nil
	case config.AuthPrivateKeyJWT:
		key, method, err := loadSigningKey(a.KeyFile, a.SigningMethod)
		if err != nil {
			return nil, err
		}
		return openfga.WithPrivateKeyJWT(openfga.PrivateKeyJWTConfig{
			TokenURL:      a.TokenURL,
			ClientID:      a.ClientID,
			Audience:      a.Audience,
			APIAudience:   a.APIAudience,
			Scopes:        a.Scopes,
			SigningKey:    key,
			SigningMethod: method,
			KeyID:         a.KeyID,
		}), nil
	default:
		return nil, fmt.Errorf("unknown auth method %q (use none, api_token, client_credentials or private_key_jwt)", a.Method)
	}
}

// loadSigningKey reads a PEM private key from path and parses it for the named
// JWT signing method (defaulting to RS256), returning the key and the resolved
// jwt.SigningMethod.
func loadSigningKey(path, methodName string) (crypto.PrivateKey, jwt.SigningMethod, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("private_key_jwt requires a key_file")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read signing key %s: %w", path, err)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintln(os.Stderr, "warning: signing key "+path+" is readable by other users; restrict it with chmod 600")
	}
	method := jwt.GetSigningMethod(methodName)
	if method == nil {
		if methodName != "" {
			return nil, nil, fmt.Errorf("unknown signing method %q (e.g. RS256, ES256, EdDSA)", methodName)
		}
		method = jwt.SigningMethodRS256
	}
	var key crypto.PrivateKey
	switch method.(type) {
	case *jwt.SigningMethodRSA, *jwt.SigningMethodRSAPSS:
		key, err = jwt.ParseRSAPrivateKeyFromPEM(pem)
	case *jwt.SigningMethodECDSA:
		key, err = jwt.ParseECPrivateKeyFromPEM(pem)
	case *jwt.SigningMethodEd25519:
		key, err = jwt.ParseEdPrivateKeyFromPEM(pem)
	default:
		return nil, nil, fmt.Errorf("unsupported signing method %q", methodName)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("signing key does not match signing_method %q: %w; for a PEM RSA key use signing_method RS256 (or PS256), for EC use ES256, for Ed25519 use EdDSA", method.Alg(), err)
	}
	return key, method, nil
}
