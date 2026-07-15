package base

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildOfgaBinary compiles the real `ofga` binary into t.TempDir() so these
// tests exercise the actual classic-CLI output path end to end (cobra's
// writers, the colorprofile wrapping, the baked banner) rather than calling
// internal functions directly.
func buildOfgaBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ofga")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/sergiught/openfga-cli/cmd/ofga")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ofga binary: %v\n%s", err, out)
	}
	return bin
}

// TestClassicCLIByteParity is the branch-review regression test: lipgloss v2
// always emits full-fidelity truecolor ANSI, so downsampling must happen at
// the writer layer (colorprofile), not at Render time. Piped output and
// NO_COLOR must both produce zero escape bytes, matching the pre-v2 binary;
// forcing color must still be honored, proving the writers aren't just
// stripping everything unconditionally.
func TestClassicCLIByteParity(t *testing.T) {
	bin := buildOfgaBinary(t)

	t.Run("piped stdout, no env overrides", func(t *testing.T) {
		out, err := exec.Command(bin, "--help").Output()
		if err != nil {
			t.Fatalf("ofga --help: %v", err)
		}
		if n := bytes.Count(out, []byte{0x1b}); n != 0 {
			t.Errorf("piped --help output has %d escape bytes, want 0:\n%s", n, out)
		}
	})

	t.Run("NO_COLOR=1", func(t *testing.T) {
		cmd := exec.Command(bin, "--help")
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("ofga --help: %v", err)
		}
		if n := bytes.Count(out, []byte{0x1b}); n != 0 {
			t.Errorf("NO_COLOR=1 --help output has %d escape bytes, want 0:\n%s", n, out)
		}
	})

	t.Run("CLICOLOR_FORCE=1 still emits truecolor", func(t *testing.T) {
		cmd := exec.Command(bin, "--help")
		cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=1", "COLORTERM=truecolor")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("ofga --help: %v", err)
		}
		if !bytes.Contains(out, []byte("38;2")) {
			t.Errorf("CLICOLOR_FORCE=1 output missing truecolor escapes, downsampling may be unconditional:\n%s", out)
		}
	})

	t.Run("--no-color overrides forced color", func(t *testing.T) {
		cmd := exec.Command(bin, "--no-color", "--help")
		cmd.Env = append(os.Environ(), "FORCE_COLOR=1", "COLORTERM=truecolor")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("ofga --no-color --help: %v", err)
		}
		if n := bytes.Count(out, []byte{0x1b}); n != 0 {
			t.Errorf("--no-color with FORCE_COLOR=1 has %d escape bytes, want 0:\n%s", n, out)
		}
	})
}
