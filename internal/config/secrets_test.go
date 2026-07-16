package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecretRoundTrip(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.toml")
	if !secretsAvailable() {
		t.Fatal("mock keyring should report available")
	}
	if err := scopedSecretSet(path, "dev", "client_secret", "shh"); err != nil {
		t.Fatal(err)
	}
	got, err := scopedSecretGet(path, "dev", "client_secret")
	if err != nil || got != "shh" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := scopedSecretDelete(path, "dev", "client_secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := scopedSecretGet(path, "dev", "client_secret"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("after delete want ErrNotFound, got %v", err)
	}
	if err := scopedSecretDelete(path, "dev", "client_secret"); err != nil {
		t.Fatalf("delete of absent key should be nil, got %v", err)
	}
}

func TestSecretsUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	if secretsAvailable() {
		t.Fatal("errored backend should report unavailable")
	}
}

func TestSecretAccountNormalizesConfigPath(t *testing.T) {
	dir := t.TempDir()
	direct := filepath.Join(dir, "config.toml")
	equivalent := filepath.Join(dir, "nested", "..", "config.toml")
	first, err := secretAccount(direct, "dev", "token")
	if err != nil {
		t.Fatal(err)
	}
	second, err := secretAccount(equivalent, "dev", "token")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent config paths produced different accounts: %q != %q", first, second)
	}
}

func TestSecretAccountResolvesConfigSymlink(t *testing.T) {
	dir := t.TempDir()
	direct := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(direct, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "config-link.toml")
	if err := os.Symlink(direct, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first, err := secretAccount(direct, "dev", "token")
	if err != nil {
		t.Fatal(err)
	}
	second, err := secretAccount(alias, "dev", "token")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("config symlink produced different accounts: %q != %q", first, second)
	}
}

func TestSecretFields(t *testing.T) {
	a := &Auth{Token: "t", ClientSecret: "c", PrivateKey: "p"}
	got := map[string]string{}
	for _, sf := range a.secretFields() {
		got[sf.name] = *sf.ptr
	}
	for _, name := range []string{"token", "client_secret", "private_key"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("secretFields missing %q", name)
		}
	}
}
