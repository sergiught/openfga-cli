package assertions

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// assertionsPayloads are the two accepted file shapes, each holding the same
// two assertions.
var assertionsPayloads = []struct {
	shape string
	data  string
}{
	{shape: "bare array", data: `[
	  {"tuple_key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"expectation":true},
	  {"tuple_key":{"user":"user:bob","relation":"viewer","object":"doc:1"},"expectation":false}
	]`},
	{shape: "wrapper object", data: `{"assertions":[
	  {"tuple_key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"expectation":true},
	  {"tuple_key":{"user":"user:bob","relation":"viewer","object":"doc:1"},"expectation":false}
	]}`},
}

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "assertions.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestAssertionsWriteFromFileAndStdin(t *testing.T) {
	for _, payload := range assertionsPayloads {
		for _, source := range []string{"file", "stdin"} {
			t.Run(payload.shape+"/"+source, func(t *testing.T) {
				s := &stubFGA{modelID: latestID}
				cmd := silenced(New(newAssertionsCLI(t, s.start(t), "json")).writeCmd())
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				if source == "stdin" {
					cmd.SetIn(strings.NewReader(payload.data))
					cmd.SetArgs([]string{"--file", "-", "--force"})
				} else {
					cmd.SetArgs([]string{"--file", writeTemp(t, payload.data), "--force"})
				}
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}

				if len(s.written) != 1 {
					t.Fatalf("wrote %d request(s), want exactly one", len(s.written))
				}
				got := s.written[0].Assertions
				if len(got) != 2 {
					t.Fatalf("wrote %d assertion(s), want 2", len(got))
				}
				if got[0].TupleKey.User != "user:anne" || !got[0].Expectation {
					t.Errorf("assertion 0 = %+v, want user:anne expecting allow", got[0])
				}
				if got[1].TupleKey.User != "user:bob" || got[1].Expectation {
					t.Errorf("assertion 1 = %+v, want user:bob expecting deny", got[1])
				}
				if s.writeModel != latestID {
					t.Errorf("assertions written to model %q, want the latest %q", s.writeModel, latestID)
				}
				if n := numField(t, decodeStructured(t, "json", out.String()), "written"); n != 2 {
					t.Errorf("written = %d, want 2", n)
				}
			})
		}
	}
}

func TestAssertionsWriteDryRunPerMode(t *testing.T) {
	for _, mode := range outputModes {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("--dry-run issued a request: %s %s", r.Method, r.URL.Path)
				http.Error(w, `{"code":"not_found","message":"unexpected"}`, http.StatusNotFound)
			}))
			defer srv.Close()

			cmd := silenced(New(newAssertionsCLI(t, srv.URL, mode)).writeCmd())
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs([]string{"--dry-run", "--file", writeTemp(t, assertionsPayloads[0].data)})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			switch mode {
			case "json", "yaml":
				m := decodeStructured(t, mode, out.String())
				if got, ok := m["dry_run"].(bool); !ok || !got {
					t.Errorf("dry_run = %#v, want true", m["dry_run"])
				}
				if got := numField(t, m, "would_write"); got != 2 {
					t.Errorf("would_write = %d, want 2", got)
				}
			case "plain":
				if want := "dry_run\ttrue\nwould_write\t2\n"; out.String() != want {
					t.Errorf("plain output = %q, want %q", out.String(), want)
				}
			case "human":
				if want := "would write 2 assertion(s)"; !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
				}
				if out.Len() != 0 {
					t.Errorf("human mode wrote to stdout: %q", out.String())
				}
			}
		})
	}
}

func TestAssertionsWriteRejectsMalformedFile(t *testing.T) {
	cmd := silenced(New(newAssertionsCLI(t, "http://127.0.0.1:0", "json")).writeCmd())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--dry-run", "--file", writeTemp(t, `{not json`)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("a malformed assertions file must fail the command")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want usage %d; err=%v", got, clierr.CodeUsage, err)
	}
}

func TestAssertionsFileReadFailureRemainsRuntimeError(t *testing.T) {
	_, err := readFileOrStdin("definitely-does-not-exist.json", &cobra.Command{})
	if got := clierr.Code(err); got != clierr.CodeError {
		t.Fatalf("missing file exit code = %d, want runtime error; err=%v", got, err)
	}
}
