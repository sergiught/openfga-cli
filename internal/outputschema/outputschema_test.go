package outputschema_test

import (
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/outputschema"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

func TestFileName(t *testing.T) {
	for path, want := range map[string]string{
		"ofga tuples write":       "tuples-write.v1.json",
		"ofga query batch-check":  "query-batch-check.v1.json",
		outputschema.EnvelopePath: "error.v1.json",
	} {
		if got := outputschema.FileName(path); got != want {
			t.Errorf("FileName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestURL(t *testing.T) {
	want := "https://sergiught.github.io/openfga-cli/schemas/output/stores-list.v1.json"
	if got := outputschema.URL("ofga stores list"); got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestEnvelopeSchemaValidatesEnvelope(t *testing.T) {
	for _, err := range []error{errors.New("x"), clierr.WithHint("try again", clierr.WithKind(clierr.KindNotFound, errors.New("gone")))} {
		raw, mErr := json.Marshal(clierr.NewEnvelope(err))
		if mErr != nil {
			t.Fatal(mErr)
		}
		outputschematest.Validate(t, outputschema.EnvelopePath, raw)
	}
}

// fakeT records a Fatalf so the negative test can observe it.
type recordingT struct {
	testing.TB
	failed bool
}

func (r *recordingT) Helper() {}
func (r *recordingT) Fatalf(string, ...any) {
	r.failed = true
	runtime.Goexit()
}

func TestValidateFailsOnViolatingDocument(t *testing.T) {
	r := &recordingT{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		outputschematest.Validate(r, "ofga stores get", []byte(`{"id":1}`))
	}()
	<-done
	if !r.failed {
		t.Fatal("Validate accepted a document that violates the schema")
	}
}
