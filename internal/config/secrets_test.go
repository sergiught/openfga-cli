package config

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecretRoundTrip(t *testing.T) {
	keyring.MockInit()
	if !secretsAvailable() {
		t.Fatal("mock keyring should report available")
	}
	if err := secretSet("dev", "client_secret", "shh"); err != nil {
		t.Fatal(err)
	}
	got, err := secretGet("dev", "client_secret")
	if err != nil || got != "shh" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := secretDelete("dev", "client_secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := secretGet("dev", "client_secret"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("after delete want ErrNotFound, got %v", err)
	}
	if err := secretDelete("dev", "client_secret"); err != nil {
		t.Fatalf("delete of absent key should be nil, got %v", err)
	}
}

func TestSecretsUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	if secretsAvailable() {
		t.Fatal("errored backend should report unavailable")
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
