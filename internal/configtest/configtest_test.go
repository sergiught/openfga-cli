package configtest

import (
	"os"
	"testing"
)

func TestIsolateClearsAgentEnv(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("OPENFGA_AGENT_MODE", "1")
	Isolate(t)
	for _, k := range []string{"CLAUDECODE", "OPENFGA_AGENT_MODE"} {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s = %q after Isolate, want empty", k, v)
		}
	}
}
