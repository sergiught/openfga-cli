package tuple

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/output"
)

// outputModes are the four renderings every bulk result has to produce.
var outputModes = []string{"json", "yaml", "plain", "human"}

// bulkKinds enumerates the two bulk commands. They share a code path but differ
// in the /write field the tuples land in and the key the result is reported
// under.
var bulkKinds = []struct {
	name   string
	newCmd func(*Command) *cobra.Command
	field  string   // result key: written / deleted
	verb   string   // human-mode success verb
	args   []string // extra flags the command needs
	sent   func(openfga.WriteRequest) (sent, unused *openfga.WriteRequestTuples)
}{
	{
		name: "write", newCmd: (*Command).writeCmd, field: "written", verb: "wrote",
		sent: func(b openfga.WriteRequest) (*openfga.WriteRequestTuples, *openfga.WriteRequestTuples) {
			return b.Writes, b.Deletes
		},
	},
	{
		name: "delete", newCmd: (*Command).deleteCmd, field: "deleted", verb: "deleted",
		args: []string{"--force"},
		sent: func(b openfga.WriteRequest) (*openfga.WriteRequestTuples, *openfga.WriteRequestTuples) {
			return b.Deletes, b.Writes
		},
	},
}

// newBulkCLI builds a CLI rendering in the given output mode.
func newBulkCLI(t *testing.T, apiURL, mode string) *cli.CLI {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newHumanTupleCLI(t, apiURL)
	switch mode {
	case "json":
		a.JSON = true
	case "yaml":
		a.YAML = true
	case "plain":
		output.Plain = true
		t.Cleanup(func() { output.Plain = false })
	}

	return a
}

// silenced mirrors how the root command runs a sub-command: cobra's own usage
// and error dumps are off, so only the command's output reaches the buffers.
func silenced(cmd *cobra.Command) *cobra.Command {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	return cmd
}

// bulkFile writes a --file payload of n tuples, numbered so the request bodies
// can be matched back to their position in the file.
func bulkFile(t *testing.T, n int) string {
	t.Helper()
	tuples := make([]tupleInput, n)
	for i := range tuples {
		tuples[i] = tupleInput{User: fmt.Sprintf("user:%d", i), Relation: "viewer", Object: "doc:1"}
	}
	data, err := json.Marshal(tuples)
	if err != nil {
		t.Fatal(err)
	}

	return writeTemp(t, string(data))
}

// writeRecorder is a /write endpoint that records the decoded request bodies
// and can be told to start failing from a given request onwards.
type writeRecorder struct {
	failFrom int // 1-based request number that starts failing; 0 never fails
	bodies   []openfga.WriteRequest
}

func (rec *writeRecorder) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body openfga.WriteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode write request: %v", err)
		}
		rec.bodies = append(rec.bodies, body)
		if rec.failFrom > 0 && len(rec.bodies) >= rec.failFrom {
			http.Error(w, `{"code":"validation_error","message":"chunk rejected"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
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

func TestBulkSplitsIntoChunksOfHundred(t *testing.T) {
	// The sizes are spelled out rather than derived from maxTuplesPerWrite:
	// OpenFGA's per-request write limit is part of the contract with the
	// server, so changing it has to be a deliberate, test-visible change.
	const total = 101
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec := &writeRecorder{}
			cmd := silenced(k.newCmd(New(newBulkCLI(t, rec.start(t), "json"))))
			cmd.SetArgs(append([]string{"--file", bulkFile(t, total)}, k.args...))
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			wantSizes := []int{100, 1}
			if len(rec.bodies) != len(wantSizes) {
				t.Fatalf("request count = %d, want %d", len(rec.bodies), len(wantSizes))
			}
			var users []string
			for i, body := range rec.bodies {
				sent, unused := k.sent(body)
				if unused != nil && len(unused.TupleKeys) > 0 {
					t.Errorf("request %d carried %d tuple(s) in the wrong field", i+1, len(unused.TupleKeys))
				}
				if sent == nil {
					t.Fatalf("request %d carried no tuples in the %s field", i+1, k.field)
				}
				if len(sent.TupleKeys) != wantSizes[i] {
					t.Errorf("request %d carried %d tuple(s), want %d", i+1, len(sent.TupleKeys), wantSizes[i])
				}
				for _, key := range sent.TupleKeys {
					users = append(users, key.User)
				}
			}
			if len(users) != total {
				t.Fatalf("sent %d tuple(s) in all, want %d", len(users), total)
			}
			for i, user := range users {
				if want := fmt.Sprintf("user:%d", i); user != want {
					t.Fatalf("tuple %d user = %q, want %q (file order must be preserved)", i, user, want)
				}
			}
			if got := numField(t, decodeStructured(t, "json", out.String()), k.field); got != total {
				t.Errorf("%s = %d, want %d", k.field, got, total)
			}
		})
	}
}

func TestBulkPartialFailureReportsTallyAndRuntimeError(t *testing.T) {
	// The first chunk commits, the second is rejected.
	const total, committed = 101, 100
	for _, k := range bulkKinds {
		for _, mode := range outputModes {
			t.Run(k.name+"/"+mode, func(t *testing.T) {
				rec := &writeRecorder{failFrom: 2}
				cmd := silenced(k.newCmd(New(newBulkCLI(t, rec.start(t), mode))))
				cmd.SetArgs(append([]string{"--file", bulkFile(t, total)}, k.args...))
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)

				err := cmd.Execute()
				if err == nil {
					t.Fatal("a failing chunk must fail the command")
				}
				want := fmt.Sprintf("tuples %d-%d failed after %d of %d tuple(s) were committed",
					committed+1, total, committed, total)
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), want)
				}
				if got := clierr.Code(err); got != clierr.CodeError {
					t.Errorf("exit code = %d, want runtime error %d", got, clierr.CodeError)
				}

				// The partial tally is emitted before the error propagates.
				switch mode {
				case "json", "yaml":
					m := decodeStructured(t, mode, out.String())
					if got := numField(t, m, k.field); got != committed {
						t.Errorf("%s = %d, want %d", k.field, got, committed)
					}
					if got := numField(t, m, "total"); got != total {
						t.Errorf("total = %d, want %d", got, total)
					}
					if got, ok := m["complete"].(bool); !ok || got {
						t.Errorf("complete = %#v, want false", m["complete"])
					}
				case "plain":
					wantOut := fmt.Sprintf("%s\t%d\ntotal\t%d\ncomplete\tfalse\n", k.field, committed, total)
					if out.String() != wantOut {
						t.Errorf("plain output = %q, want %q", out.String(), wantOut)
					}
				case "human":
					if out.Len() != 0 {
						t.Errorf("human mode wrote to stdout: %q", out.String())
					}
				}
			})
		}
	}
}

func TestBulkSuccessOutputPerMode(t *testing.T) {
	const total = 2
	for _, k := range bulkKinds {
		for _, mode := range outputModes {
			t.Run(k.name+"/"+mode, func(t *testing.T) {
				rec := &writeRecorder{}
				cmd := silenced(k.newCmd(New(newBulkCLI(t, rec.start(t), mode))))
				cmd.SetArgs(append([]string{"--file", bulkFile(t, total)}, k.args...))
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(rec.bodies) != 1 {
					t.Fatalf("request count = %d, want a single request", len(rec.bodies))
				}

				switch mode {
				case "json", "yaml":
					if got := numField(t, decodeStructured(t, mode, out.String()), k.field); got != total {
						t.Errorf("%s = %d, want %d", k.field, got, total)
					}
				case "plain":
					if want := fmt.Sprintf("%s\t%d\n", k.field, total); out.String() != want {
						t.Errorf("plain output = %q, want %q", out.String(), want)
					}
				case "human":
					if want := fmt.Sprintf("%s %d tuple(s)", k.verb, total); !strings.Contains(errOut.String(), want) {
						t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
					}
					if out.Len() != 0 {
						t.Errorf("human mode wrote to stdout: %q", out.String())
					}
				}
			})
		}
	}
}

func TestBulkDryRunMakesNoRequest(t *testing.T) {
	const total = 2
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec := &writeRecorder{}
			cmd := silenced(k.newCmd(New(newBulkCLI(t, rec.start(t), "json"))))
			cmd.SetArgs(append([]string{"--dry-run", "--file", bulkFile(t, total)}, k.args...))
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			if len(rec.bodies) != 0 {
				t.Fatalf("--dry-run issued %d request(s), want none", len(rec.bodies))
			}
			m := decodeStructured(t, "json", out.String())
			if got, ok := m["dry_run"].(bool); !ok || !got {
				t.Errorf("dry_run = %#v, want true", m["dry_run"])
			}
			if got := numField(t, m, "would_"+k.name); got != total {
				t.Errorf("would_%s = %d, want %d", k.name, got, total)
			}
		})
	}
}

func TestSingleTupleSentInTheRightField(t *testing.T) {
	for _, k := range bulkKinds {
		t.Run(k.name, func(t *testing.T) {
			rec := &writeRecorder{}
			cmd := silenced(k.newCmd(New(newBulkCLI(t, rec.start(t), "json"))))
			cmd.SetArgs(append([]string{"user:anne", "viewer", "doc:1"}, k.args...))
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			if len(rec.bodies) != 1 {
				t.Fatalf("request count = %d, want a single request", len(rec.bodies))
			}
			sent, unused := k.sent(rec.bodies[0])
			if unused != nil && len(unused.TupleKeys) > 0 {
				t.Errorf("tuple landed in the wrong field: %+v", unused.TupleKeys)
			}
			if sent == nil || len(sent.TupleKeys) != 1 {
				t.Fatalf("%s field = %+v, want exactly one tuple", k.field, sent)
			}
			if got := sent.TupleKeys[0]; got.User != "user:anne" || got.Relation != "viewer" || got.Object != "doc:1" {
				t.Errorf("tuple = %+v, want user:anne viewer doc:1", got)
			}
			if got := numField(t, decodeStructured(t, "json", out.String()), k.field); got != 1 {
				t.Errorf("%s = %d, want 1", k.field, got)
			}
		})
	}
}
