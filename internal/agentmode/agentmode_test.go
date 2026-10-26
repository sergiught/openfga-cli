package agentmode

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name   string
		flag   *bool
		env    map[string]string
		want   bool
		reason string
	}{
		{name: "nothing", want: false, reason: ""},
		{name: "claude code", env: map[string]string{"CLAUDECODE": "1"}, want: true, reason: "CLAUDECODE=1"},
		{name: "gemini cli", env: map[string]string{"GEMINI_CLI": "1"}, want: true, reason: "GEMINI_CLI=1"},
		{name: "env opt-out beats detection", env: map[string]string{"CLAUDECODE": "1", "OPENFGA_AGENT_MODE": "0"}, want: false, reason: "OPENFGA_AGENT_MODE=0"},
		{name: "env opt-in", env: map[string]string{"OPENFGA_AGENT_MODE": "1"}, want: true, reason: "OPENFGA_AGENT_MODE=1"},
		{name: "flag beats env", flag: &yes, env: map[string]string{"OPENFGA_AGENT_MODE": "0"}, want: true, reason: "--agent"},
		{name: "flag false beats detection", flag: &no, env: map[string]string{"CLAUDECODE": "1"}, want: false, reason: "--agent=false"},
		{name: "unparsable env ignored", env: map[string]string{"OPENFGA_AGENT_MODE": "banana"}, want: false, reason: ""},
		{name: "unparsable env falls to detection", env: map[string]string{"OPENFGA_AGENT_MODE": "banana", "CLAUDECODE": "1"}, want: true, reason: "CLAUDECODE=1"},
		{name: "empty detector", env: map[string]string{"CLAUDECODE": ""}, want: false, reason: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.flag, func(k string) string { return tt.env[k] })
			if got.Enabled != tt.want || got.Reason != tt.reason {
				t.Fatalf("Resolve() = %+v, want enabled=%v reason=%q", got, tt.want, tt.reason)
			}
		})
	}
}

func TestEnvVarsListsEveryDetector(t *testing.T) {
	have := map[string]bool{}
	for _, v := range EnvVars() {
		have[v] = true
	}
	for _, want := range []string{"OPENFGA_AGENT_MODE", "CLAUDECODE", "GEMINI_CLI"} {
		if !have[want] {
			t.Errorf("EnvVars() missing %s", want)
		}
	}
}

func TestRefuseHintMatchesReason(t *testing.T) {
	flagErr := State{Enabled: true, Reason: "--agent"}.Refuse("the playground").Error()
	if !strings.Contains(flagErr, "drop --agent") || strings.Contains(flagErr, EnvOverride) {
		t.Errorf("--agent refusal must hint at dropping the flag: %q", flagErr)
	}
	envErr := State{Enabled: true, Reason: "CLAUDECODE=1"}.Refuse("the playground").Error()
	if !strings.Contains(envErr, "set OPENFGA_AGENT_MODE=0") {
		t.Errorf("env refusal must hint at the override: %q", envErr)
	}
}
