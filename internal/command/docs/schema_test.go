package docs

import (
	"sync/atomic"
	"testing"

	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each docs command with --json against the
// httptest seam (no network) and checks the document against the schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	var n atomic.Int32
	srv := fakeDocs(t, twoHits, &n)
	for _, tc := range []struct {
		path string
		args []string
	}{
		{"ofga docs search", []string{"search", "conditions"}},
		{"ofga docs show", []string{"show", "https://openfga.dev/docs/modeling/conditions"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			out, _, err := run(t, srv, true, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			outputschematest.Validate(t, tc.path, []byte(out))
		})
	}
}
