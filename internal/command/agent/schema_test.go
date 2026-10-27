package agent

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each agent command with --json and checks
// the document against its schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	for _, tc := range []struct {
		path string
		args []string
	}{
		{"ofga agent skill show", []string{"skill", "show"}},
		{"ofga agent skill install", []string{"skill", "install", "--dir", t.TempDir()}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			out, _, err := run(t, true, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			outputschematest.Validate(t, tc.path, []byte(out))
		})
	}
}
