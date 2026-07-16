package readlimit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllRejectsOversizedInput(t *testing.T) {
	if _, err := All(strings.NewReader("12345"), 4, "payload"); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("All() error = %v, want size limit", err)
	}

}

func TestSecretPermissionWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := SecretPermissionWarning(path, "token file"); !strings.Contains(warning, "chmod 600") {
		t.Fatalf("warning = %q", warning)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if warning := SecretPermissionWarning(path, "token file"); warning != "" {
		t.Fatalf("secure file warning = %q", warning)
	}
}

func TestAllAcceptsLimitExactly(t *testing.T) {
	got, err := All(strings.NewReader("1234"), 4, "payload")
	if err != nil || string(got) != "1234" {
		t.Fatalf("All() = %q, %v", got, err)
	}
}
