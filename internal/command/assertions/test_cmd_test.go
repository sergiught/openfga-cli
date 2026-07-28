package assertions

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/output"
)

const (
	storeID  = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	latestID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
)

// outputModes are the four renderings every assertions result has to produce.
var outputModes = []string{"json", "yaml", "plain", "human"}

// stubFGA serves the endpoints the assertions commands touch: the latest-model
// lookup, the stored assertions, the Check per assertion, and the assertions
// replacement. It records what it was asked for so tests can assert on it.
type stubFGA struct {
	modelID     string
	assertions  []openfga.Assertion
	allowed     map[string]bool // "user relation object" -> Check result
	checkStatus int             // non-zero makes every Check fail with this status

	checks     []openfga.CheckRequest
	written    []openfga.WriteAssertionsRequest
	readModel  string // model id the assertions were read for
	writeModel string // model id the assertions were written to
}

func (s *stubFGA) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/authorization-models"):
			_ = json.NewEncoder(w).Encode(openfga.ListAuthorizationModelsResponse{
				AuthorizationModels: []openfga.AuthorizationModel{{ID: s.modelID}},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/assertions/"):
			s.readModel = path.Base(r.URL.Path)
			_ = json.NewEncoder(w).Encode(openfga.ReadAssertionsResponse{
				AuthorizationModelID: s.modelID, Assertions: s.assertions,
			})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assertions/"):
			s.writeModel = path.Base(r.URL.Path)
			var req openfga.WriteAssertionsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode write assertions body: %v", err)
			}
			s.written = append(s.written, req)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/check"):
			var req openfga.CheckRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode check body: %v", err)
			}
			s.checks = append(s.checks, req)
			if s.checkStatus != 0 {
				http.Error(w, `{"code":"validation_error","message":"check exploded"}`, s.checkStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(openfga.CheckResponse{Allowed: s.allowed[assertionKey(req.TupleKey)]})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"code":"not_found","message":"unexpected"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func assertionKey(k openfga.CheckRequestTupleKey) string {
	return k.User + " " + k.Relation + " " + k.Object
}

// newAssertionsCLI builds a CLI rendering in the given output mode.
func newAssertionsCLI(t *testing.T, apiURL, mode string) *cli.CLI {
	t.Helper()
	configtest.Isolate(t)
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: storeID})
	a := cli.New(log.New(io.Discard), cfg, "test")
	switch mode {
	case "json":
		a.JSON = true
	case "yaml":
		a.YAML = true
	}
	// Set unconditionally rather than only for "plain": the human-mode cases
	// assert on exact rendering, which would silently change if some other test
	// in the package ever leaked output.Plain = true.
	output.Plain = mode == "plain"
	t.Cleanup(func() { output.Plain = false })

	return a
}

// silenced mirrors how the root command runs a sub-command: cobra's own usage
// and error dumps are off, so only the command's output reaches the buffers.
func silenced(cmd *cobra.Command) *cobra.Command {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	return cmd
}

// decodeStructured decodes a --json or --yaml payload into a map.
func decodeStructured(t *testing.T, mode, out string) map[string]any {
	t.Helper()
	m := map[string]any{}
	var err error
	if mode == "yaml" {
		err = yaml.Unmarshal([]byte(out), &m)
	} else {
		err = json.Unmarshal([]byte(out), &m)
	}
	if err != nil {
		t.Fatalf("decode %s output %q: %v", mode, out, err)
	}

	return m
}

// numField reads a numeric field from decoded output (JSON decodes numbers as
// float64, YAML as int).
func numField(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("field %q = %#v, want a number", key, m[key])
		return 0
	}
}

// passingAssertions are two assertions whose stored expectation matches the
// Check answers in passingAnswers.
var passingAssertions = []openfga.Assertion{
	{TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"}, Expectation: true},
	{TupleKey: openfga.CheckRequestTupleKey{User: "user:bob", Relation: "viewer", Object: "doc:1"}, Expectation: false},
}

var passingAnswers = map[string]bool{"user:anne viewer doc:1": true}

func TestAssertionsTestHappyPathPerMode(t *testing.T) {
	for _, mode := range outputModes {
		t.Run(mode, func(t *testing.T) {
			s := &stubFGA{modelID: latestID, assertions: passingAssertions, allowed: passingAnswers}
			cmd := silenced(New(newAssertionsCLI(t, s.start(t), mode)).testCmd())
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			// The model is resolved to the latest, and every Check runs against it.
			if s.readModel != latestID {
				t.Errorf("assertions read for model %q, want the latest %q", s.readModel, latestID)
			}
			if len(s.checks) != len(passingAssertions) {
				t.Fatalf("ran %d check(s), want one per assertion (%d)", len(s.checks), len(passingAssertions))
			}
			for i, chk := range s.checks {
				if want := passingAssertions[i].TupleKey; chk.TupleKey != want {
					t.Errorf("check %d tuple = %+v, want %+v", i, chk.TupleKey, want)
				}
				if chk.AuthorizationModelID != latestID {
					t.Errorf("check %d ran against model %q, want %q", i, chk.AuthorizationModelID, latestID)
				}
			}

			switch mode {
			case "json", "yaml":
				m := decodeStructured(t, mode, out.String())
				if got := m["authorization_model_id"]; got != latestID {
					t.Errorf("authorization_model_id = %v, want %q", got, latestID)
				}
				if got := numField(t, m, "passed"); got != 2 {
					t.Errorf("passed = %d, want 2", got)
				}
				if got := numField(t, m, "total"); got != 2 {
					t.Errorf("total = %d, want 2", got)
				}
				results, ok := m["results"].([]any)
				if !ok || len(results) != 2 {
					t.Fatalf("results = %#v, want 2 entries", m["results"])
				}
				first, ok := results[0].(map[string]any)
				if !ok {
					t.Fatalf("result 0 = %#v, want an object", results[0])
				}
				if got := first["assertion"]; got != "user:anne viewer doc:1" {
					t.Errorf("result 0 assertion = %v, want %q", got, "user:anne viewer doc:1")
				}
				for _, key := range []string{"expected", "got", "pass"} {
					if v, ok := first[key].(bool); !ok || !v {
						t.Errorf("result 0 %s = %#v, want true", key, first[key])
					}
				}
			case "plain":
				want := "true\tuser:anne viewer doc:1\ttrue\ttrue\ntrue\tuser:bob viewer doc:1\tfalse\tfalse\n"
				if out.String() != want {
					t.Errorf("plain output = %q, want %q", out.String(), want)
				}
			case "human":
				for _, want := range []string{"user:anne viewer doc:1", "user:bob viewer doc:1", "2/2 passed"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("output = %q, want it to contain %q", out.String(), want)
					}
				}
			}
		})
	}
}

func TestAssertionsTestFailuresExitTestFailedAfterFullTable(t *testing.T) {
	// Two pass, one fails: anne is allowed as expected, bob is expected to be
	// denied but the server allows him, carol matches her expectation.
	mixed := []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "doc:1"}, Expectation: true},
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:bob", Relation: "viewer", Object: "doc:1"}, Expectation: false},
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:carol", Relation: "viewer", Object: "doc:1"}, Expectation: false},
	}
	answers := map[string]bool{"user:anne viewer doc:1": true, "user:bob viewer doc:1": true}

	for _, mode := range outputModes {
		t.Run(mode, func(t *testing.T) {
			s := &stubFGA{modelID: latestID, assertions: mixed, allowed: answers}
			cmd := silenced(New(newAssertionsCLI(t, s.start(t), mode)).testCmd())
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(nil)

			err := cmd.Execute()
			if got := clierr.Code(err); got != clierr.CodeTestFailed {
				t.Fatalf("exit code = %d, want %d; err=%v", got, clierr.CodeTestFailed, err)
			}
			if !strings.Contains(err.Error(), "1 assertion(s) failed") {
				t.Errorf("error = %q, want it to report one failure", err.Error())
			}
			// Every assertion is reported before the failure propagates.
			for _, a := range mixed {
				if !strings.Contains(out.String(), a.TupleKey.User) {
					t.Errorf("output = %q, want a row for %s", out.String(), a.TupleKey.User)
				}
			}

			switch mode {
			case "json", "yaml":
				m := decodeStructured(t, mode, out.String())
				if got := numField(t, m, "passed"); got != 2 {
					t.Errorf("passed = %d, want 2", got)
				}
				if got := numField(t, m, "total"); got != 3 {
					t.Errorf("total = %d, want 3", got)
				}
			case "plain":
				lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
				if len(lines) != len(mixed) {
					t.Fatalf("plain output = %q, want one row per assertion", out.String())
				}
				if got := strings.Split(lines[1], "\t")[0]; got != "false" {
					t.Errorf("bob's row pass column = %q, want false", got)
				}
			case "human":
				if !strings.Contains(out.String(), "2/3 passed") {
					t.Errorf("output = %q, want it to contain %q", out.String(), "2/3 passed")
				}
			}
		})
	}
}

func TestAssertionsTestWithNoAssertionsIsNotAFailure(t *testing.T) {
	s := &stubFGA{modelID: latestID}
	cmd := silenced(New(newAssertionsCLI(t, s.start(t), "human")).testCmd())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("an empty assertion suite must not fail: %v", err)
	}

	if len(s.checks) != 0 {
		t.Errorf("ran %d check(s) with no assertions, want none", len(s.checks))
	}
	if want := "no assertions to run for model " + latestID; !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
	}
	if out.Len() != 0 {
		t.Errorf("notice leaked onto stdout: %q", out.String())
	}
}

func TestAssertionsTestWrapsCheckFailure(t *testing.T) {
	s := &stubFGA{
		modelID:     latestID,
		assertions:  passingAssertions,
		checkStatus: http.StatusBadRequest,
	}
	cmd := silenced(New(newAssertionsCLI(t, s.start(t), "json")).testCmd())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("a failing check must fail the command")
	}
	if !strings.HasPrefix(err.Error(), "check user:anne viewer doc:1: ") {
		t.Errorf("error = %q, want it to name the failing tuple", err.Error())
	}
	if out.Len() != 0 {
		t.Errorf("partial results leaked onto stdout: %q", out.String())
	}
}

func TestAssertionsTestUsesExplicitModelID(t *testing.T) {
	const explicit = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	s := &stubFGA{modelID: latestID, assertions: passingAssertions, allowed: passingAnswers}
	cmd := silenced(New(newAssertionsCLI(t, s.start(t), "json")).testCmd())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{explicit})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if s.readModel != explicit {
		t.Errorf("assertions read for model %q, want the explicit %q", s.readModel, explicit)
	}
	for i, chk := range s.checks {
		if chk.AuthorizationModelID != explicit {
			t.Errorf("check %d ran against model %q, want %q", i, chk.AuthorizationModelID, explicit)
		}
	}
}

func TestAssertionsTestReportsLatestModelLookupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"code":"validation_error","message":"models unavailable"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	cmd := silenced(New(newAssertionsCLI(t, srv.URL, "json")).testCmd())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("an unresolvable latest model must fail the command")
	}
	if !strings.HasPrefix(err.Error(), "resolve latest model: ") {
		t.Errorf("error = %q, want it to name the failed model lookup", err.Error())
	}
}
