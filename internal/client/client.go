// Package client constructs a configured go-openfga client from resolved config.
package client

import (
	"crypto"
	"fmt"
	"os"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
)

// New builds an *openfga.Client from a resolved configuration. The store and
// authorization-model IDs are registered as client defaults so per-call
// overrides remain optional.
func New(r config.Resolved) (*openfga.Client, error) {
	if r.APIURL == "" {
		return nil, fmt.Errorf("no API URL configured: set one with --api-url, OPENFGA_API_URL, or `ofga profiles set`")
	}

	opts := []openfga.Option{
		openfga.WithUserAgent("ofga-cli"),
	}
	if r.StoreID != "" {
		opts = append(opts, openfga.WithStoreID(r.StoreID))
	}
	if r.ModelID != "" {
		opts = append(opts, openfga.WithAuthorizationModelID(r.ModelID))
	}
	authOpt, err := authOption(r.Auth)
	if err != nil {
		return nil, err
	}
	if authOpt != nil {
		opts = append(opts, authOpt)
	}

	c, err := openfga.NewClient(r.APIURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("create openfga client: %w", err)
	}
	return c, nil
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
	method := jwt.GetSigningMethod(methodName)
	if method == nil {
		method = jwt.SigningMethodRS256
	}
	switch method.(type) {
	case *jwt.SigningMethodRSA, *jwt.SigningMethodRSAPSS:
		key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
		return key, method, err
	case *jwt.SigningMethodECDSA:
		key, err := jwt.ParseECPrivateKeyFromPEM(pem)
		return key, method, err
	case *jwt.SigningMethodEd25519:
		key, err := jwt.ParseEdPrivateKeyFromPEM(pem)
		return key, method, err
	default:
		return nil, nil, fmt.Errorf("unsupported signing method %q", methodName)
	}
}
