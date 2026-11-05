package playground

import (
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/optimize"
)

// TestOptimizerDisplay_StripsTerminalEscapes pins that model-derived text in
// the optimizer's review card and write dialog cannot replay terminal
// control sequences, while the stored finding keeps its raw values.
func TestOptimizerDisplay_StripsTerminalEscapes(t *testing.T) {
	const osc52 = "\x1b]52;c;aGk=\x07"
	f := optimize.Finding{
		ID: "S1", Type: "doc" + osc52, Relation: "viewer\x1b[2J",
		Before: "a" + osc52 + "\nb", After: "b" + osc52, Reason: "r" + osc52, Proof: "p" + osc52,
		Weights: map[string]int{"user" + osc52: 1},
	}
	d := displayFinding(f)
	lines := dialogBody([]optimize.Finding{f}, "model\n# "+osc52+"\n", "model\n")
	all := strings.Join([]string{d.Type, d.Relation, d.Before, d.After, d.Reason, d.Proof, strings.Join(lines, "\n")}, "\n")
	for k := range d.Weights {
		all += k
	}
	if strings.Contains(all, "]52;") || strings.Contains(all, "\x1b[2J") || strings.Contains(all, "\x07") {
		t.Errorf("display still carries an escape:\n%q", all)
	}
	if d.Before != "a\nb" {
		t.Errorf("Before = %q, want line break kept", d.Before)
	}
	if !strings.Contains(f.Type, osc52) {
		t.Error("displayFinding mutated the stored finding")
	}
}
