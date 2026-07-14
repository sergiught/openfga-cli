package client

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/config"
)

// TestPlaintextCredentialWarning covers AUTH-2: a warning is emitted only when
// credentials would travel over cleartext http to a non-loopback host.
func TestPlaintextCredentialWarning(t *testing.T) {
	tests := []struct {
		name    string
		apiURL  string
		auth    config.Auth
		wantMsg bool
	}{
		{"http non-loopback with token", "http://api.example.com:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, true},
		{"http non-loopback client secret", "http://192.168.1.10:8080", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s"}, true},
		{"https non-loopback with token", "https://api.example.com", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http localhost", "http://localhost:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http 127.0.0.1", "http://127.0.0.1:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http ipv6 loopback", "http://[::1]:8080", config.Auth{Method: config.AuthAPIToken, Token: "secret"}, false},
		{"http non-loopback no auth", "http://api.example.com", config.Auth{Method: config.AuthNone}, false},
		{"http non-loopback empty method", "http://api.example.com", config.Auth{}, false},
		// AUTH-8: api_token with no token sends no credential, so no warning.
		{"http non-loopback empty token", "http://api.example.com", config.Auth{Method: config.AuthAPIToken}, false},
		// AUTH-7: the client secret / signed assertion travels to the token
		// endpoint, so a cleartext token_url must warn even when the API is https.
		{"https api, http token_url client secret", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "http://issuer.example.com/oauth/token"}, true},
		{"https api, http token_url private key", "https://api.example.com", config.Auth{Method: config.AuthPrivateKeyJWT, KeyFile: "/k.pem", TokenURL: "http://issuer.example.com/oauth/token"}, true},
		{"https api and https token_url", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "https://issuer.example.com/oauth/token"}, false},
		{"http api, https token_url still warns on bearer", "http://api.example.com", config.Auth{Method: config.AuthClientCredentials, ClientSecret: "s", TokenURL: "https://issuer.example.com/oauth/token"}, true},
		{"client credentials no secret", "https://api.example.com", config.Auth{Method: config.AuthClientCredentials, TokenURL: "http://issuer.example.com/oauth/token"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plaintextCredentialWarning(tc.apiURL, tc.auth)
			if tc.wantMsg && got == "" {
				t.Fatalf("expected a warning, got none")
			}
			if !tc.wantMsg && got != "" {
				t.Fatalf("expected no warning, got %q", got)
			}
		})
	}
}

// TestLoadSigningKeyMethodMismatch covers AUTH-5: a key/method mismatch yields an
// actionable error instead of an opaque parse failure.
func TestLoadSigningKeyMethodMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(rsaKey)
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// RSA key parsed as an EC method must fail with the actionable message.
	if _, _, err := loadSigningKey(path, "ES256"); err == nil {
		t.Fatal("expected mismatch error, got nil")
	} else if !strings.Contains(err.Error(), `does not match signing_method "ES256"`) {
		t.Fatalf("error missing actionable context: %v", err)
	}

	// RSA key with a matching method must succeed.
	if _, _, err := loadSigningKey(path, "RS256"); err != nil {
		t.Fatalf("expected RSA/RS256 to succeed, got %v", err)
	}
}
