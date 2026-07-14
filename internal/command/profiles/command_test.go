package profiles

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

// setRunE returns the RunE of the `profiles set` subcommand, wired to a fresh
// in-memory config (a default profile is active), so validation can be tested
// without touching disk — the error paths return before SaveConfig.
func setRunE(t *testing.T) (func(*cobra.Command, []string) error, *cli.CLI) {
	t.Helper()
	c := cli.New(charmlog.New(io.Discard), config.New(), "test")
	group := New(c).Command()
	for _, sub := range group.Commands() {
		if sub.Name() == "set" {
			return sub.RunE, c
		}
	}
	t.Fatal("set subcommand not found")
	return nil, nil
}

func TestProfilesSetValidation(t *testing.T) {
	run, _ := setRunE(t)
	cmd := &cobra.Command{}

	if err := run(cmd, []string{"auth_method", "bogus"}); err == nil {
		t.Error("invalid auth_method should be rejected")
	}
	if err := run(cmd, []string{"nonsense_key", "x"}); err == nil {
		t.Error("unknown key should be rejected")
	}
	// A secret passed as a literal argument is refused (CFG-7).
	if err := run(cmd, []string{"token", "sekret"}); err == nil {
		t.Error("token as a literal argument should be refused")
	}
	if err := run(cmd, []string{"client_secret", "sekret"}); err == nil {
		t.Error("client_secret as a literal argument should be refused")
	}
}

// subCmd returns a named `profiles` subcommand wired to c, so its flags can be
// set and its RunE invoked directly without touching disk.
func subCmd(t *testing.T, c *cli.CLI, name string) *cobra.Command {
	t.Helper()
	for _, sub := range New(c).Command().Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("%s subcommand not found", name)
	return nil
}

func TestProfilesAddRefusesLiteralSecrets(t *testing.T) {
	t.Run("token", func(t *testing.T) {
		add := subCmd(t, cli.New(charmlog.New(io.Discard), config.New(), "test"), "add")
		if err := add.Flags().Set("token", "sekret"); err != nil {
			t.Fatal(err)
		}
		if err := add.RunE(add, []string{"new"}); err == nil {
			t.Error("add with a literal --token should be refused")
		}
	})
	t.Run("client-secret", func(t *testing.T) {
		add := subCmd(t, cli.New(charmlog.New(io.Discard), config.New(), "test"), "add")
		if err := add.Flags().Set("client-secret", "sekret"); err != nil {
			t.Fatal(err)
		}
		if err := add.RunE(add, []string{"new"}); err == nil {
			t.Error("add with a literal --client-secret should be refused")
		}
	})
}

func TestProfilesRemoveRefusesEnvActiveProfile(t *testing.T) {
	c := cli.New(charmlog.New(io.Discard), &config.Config{
		Active: "default",
		Profiles: map[string]config.Profile{
			"default": {APIURL: config.DefaultAPIURL},
			"prod":    {APIURL: "http://prod:8080"},
		},
	}, "test")
	remove := subCmd(t, c, "remove")

	// prod is selected via the environment; removing it must be refused even
	// though it is not the file's active_profile. The guard returns before the
	// confirmation prompt, so no --force is needed.
	t.Setenv("OPENFGA_PROFILE", "prod")
	if err := remove.RunE(remove, []string{"prod"}); err == nil {
		t.Error("removing the env-selected active profile should be refused")
	}
}

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
