package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecret(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("filetoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		literal   string
		file      string
		fromStdin bool
		stdin     string
		want      string
		wantErr   bool
	}{
		{name: "literal", literal: "littoken", want: "littoken"},
		{name: "file trims trailing newline", file: file, want: "filetoken"},
		{name: "stdin trims whitespace", fromStdin: true, stdin: "  stdintoken\n", want: "stdintoken"},
		{name: "empty is allowed", want: ""},
		{name: "two sources rejected", literal: "x", fromStdin: true, stdin: "y", wantErr: true},
		{name: "missing file errors", file: filepath.Join(dir, "nope"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSecret(strings.NewReader(tt.stdin), tt.literal, tt.file, tt.fromStdin)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readSecret() expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readSecret() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("readSecret() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTokenStateNeverLeaksSecret(t *testing.T) {
	const secret = "supersecrettoken123"
	got := tokenState(secret)
	if strings.Contains(got, secret[:3]) || strings.Contains(got, secret[len(secret)-3:]) {
		t.Errorf("tokenState(%q) = %q leaks characters of the secret", secret, got)
	}
	if got == secret {
		t.Errorf("tokenState returned the raw secret")
	}
	if tokenState("") != "—" {
		t.Errorf("tokenState(\"\") = %q, want em dash", tokenState(""))
	}
}
