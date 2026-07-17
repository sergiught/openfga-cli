package base

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestPlaygroundSubcommandRegistered(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()
	cmd, _, err := root.Find([]string{"playground"})
	if err != nil {
		t.Fatalf("find playground command: %v", err)
	}
	if cmd == root || cmd.Name() != "playground" {
		t.Fatal("expected `ofga playground` to launch the TUI explicitly")
	}
}

func TestApplySecretFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := cli.New(log.New(io.Discard), config.New(), "test")
	c := New(a)
	a.APITokenFile = write("token", "api-secret")
	a.ClientSecretFile = write("client", "client-secret")
	a.PrivateKeyFile = write("key", "PEM")
	if err := c.applySecretFiles(); err != nil {
		t.Fatal(err)
	}
	if a.Overrides.APIToken != "api-secret" || a.Overrides.ClientSecret != "client-secret" || a.Overrides.PrivateKey != "PEM" {
		t.Fatalf("runtime secrets were not loaded: %+v", a.Overrides)
	}
}

func TestApplySecretFilesWarnsAboutPermissiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := cli.New(log.New(io.Discard), config.New(), "test")
	c := New(a)
	var stderr bytes.Buffer
	c.cmd.SetErr(&stderr)
	a.APITokenFile = path

	if err := c.applySecretFiles(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "chmod 600") {
		t.Fatalf("stderr = %q, want permission warning", stderr.String())
	}
}

// TestFirstUnknownFlag guards CLI-31: a mistyped subcommand that carries a flag
// must not be misreported as an "unknown flag". FirstUnknownFlag must stop at
// the first positional (the intended command path) so cobra's own "unknown
// command"/did-you-mean diagnosis stands, while a genuinely unknown global flag
// that precedes any subcommand is still detected (the original CLI-27 case).
func TestFirstUnknownFlag(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()

	cases := []struct {
		name string
		args []string
		want string
	}{
		// CLI-27: unknown global flag before any subcommand is still reported.
		{"unknown long flag before command", []string{"--debu", "stores", "list"}, "--debu"},
		{"unknown value flag before command", []string{"--profil", "x", "stores", "list"}, "--profil"},
		{"unknown flag with inline value", []string{"--bogus=1", "stores"}, "--bogus"},
		// CLI-31: a flag after a (mistyped) subcommand belongs to that subcommand.
		{"mistyped command with unknown flag", []string{"model", "transform", "--file", "x"}, ""},
		{"mistyped command with valid flag", []string{"stores", "creat", "--name", "foo"}, ""},
		{"mistyped command with file flag", []string{"tuples", "ad", "--file", "x"}, ""},
		// Known global flags consume their value token, then the positional stops the scan.
		{"known value flag then mistyped command", []string{"--profile", "local", "stores", "creat", "--name", "x"}, ""},
		{"known shorthand value then mistyped command", []string{"-p", "local", "stores", "creat"}, ""},
		{"known inline value flag then command", []string{"--profile=local", "stores", "creat"}, ""},
		{"known bool flag then command", []string{"--no-color", "stores", "list"}, ""},
		{"known theme flag consumes value", []string{"--theme", "dark", "model", "transfrm"}, ""},
		{"known debug flag then command", []string{"--debug", "stores", "list"}, ""},
		// Terminator and no-flag cases.
		{"terminator", []string{"--", "--file"}, ""},
		{"bare command", []string{"stores", "list"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstUnknownFlag(root, tc.args); got != tc.want {
				t.Fatalf("FirstUnknownFlag(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestUnknownSubcommandDiagnosis is the end-to-end counterpart to
// TestFirstUnknownFlag: it drives the real binary (which applies the
// FirstUnknownFlag override in main) to confirm CLI-31 both ways. `model`
// whitelists unknown flags (FParseErrWhitelist), so cobra reaches its "unknown
// command" diagnosis — which the override must no longer clobber — while a
// genuinely unknown global flag before any command must still be reported.
func TestUnknownSubcommandDiagnosis(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"mistyped subcommand keeps unknown command", []string{"model", "transform", "--file", "x"}, `unknown command "transform" for "ofga model"`},
		{"known flag value then mistyped subcommand", []string{"--profile", "local", "model", "transfrm"}, `unknown command "transfrm" for "ofga model"`},
		{"unknown global flag still reported", []string{"--debu", "stores", "list"}, "unknown flag: --debu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errb, code := runOfga(t, home, "", nil, tc.args...)
			if code != clierr.CodeUsage {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", code, clierr.CodeUsage, errb)
			}
			if !strings.Contains(errb, tc.wantErr) {
				t.Fatalf("stderr = %q, want it to contain %q", errb, tc.wantErr)
			}
		})
	}
}

// TestApplyEnvironmentRejectsUnknownThemeFlag guards CLI-34: an explicit
// --theme with an unknown value is a usage error, while a bad theme coming only
// from the config file keeps its silent fallback.
func TestApplyEnvironmentRejectsUnknownThemeFlag(t *testing.T) {
	// Neutralize the ambient color environment so applyEnvironment reaches the
	// theme branch instead of short-circuiting on NO_COLOR/mono.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("FORCE_COLOR", "")

	t.Run("explicit bogus flag errors", func(t *testing.T) {
		a := cli.New(log.New(io.Discard), config.New(), "test")
		a.ThemeName = "does-not-exist"
		err := New(a).applyEnvironment()
		if err == nil {
			t.Fatal("expected an error for an unknown --theme value")
		}
		if code := clierr.Code(err); code != clierr.CodeUsage {
			t.Fatalf("clierr.Code = %d, want %d", code, clierr.CodeUsage)
		}
		if !strings.Contains(err.Error(), "unknown theme") {
			t.Fatalf("error = %q, want it to mention the unknown theme", err.Error())
		}
	})

	t.Run("valid explicit flag succeeds", func(t *testing.T) {
		a := cli.New(log.New(io.Discard), config.New(), "test")
		a.ThemeName = "mono"
		if err := New(a).applyEnvironment(); err != nil {
			t.Fatalf("valid --theme mono returned error: %v", err)
		}
	})

	t.Run("bogus config theme falls back silently", func(t *testing.T) {
		a := cli.New(log.New(io.Discard), config.New(), "test")
		a.Config.Theme = "does-not-exist"
		if err := New(a).applyEnvironment(); err != nil {
			t.Fatalf("bogus config theme should not error: %v", err)
		}
	})
}

func TestExampleLinesPreserveRelativeIndentation(t *testing.T) {
	lines := exampleLines("  ofga profiles add ci \\\n    --client-id abc \\\n    --token-url https://issuer.example")
	if len(lines) != 3 {
		t.Fatalf("exampleLines returned %d lines", len(lines))
	}
	if lines[0].text != "ofga profiles add ci \\" {
		t.Fatalf("first line = %q", lines[0].text)
	}
	if lines[1].text != "  --client-id abc \\" || lines[2].text != "  --token-url https://issuer.example" {
		t.Fatalf("continuation indentation lost: %#v", lines)
	}
}
