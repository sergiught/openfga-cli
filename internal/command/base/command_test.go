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
