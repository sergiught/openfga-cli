package query

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

type batchCheckBody struct {
	Checks []struct {
		TupleKey         map[string]any `json:"tuple_key"`
		CorrelationID    string         `json:"correlation_id"`
		ContextualTuples struct {
			TupleKeys []map[string]any `json:"tuple_keys"`
		} `json:"contextual_tuples"`
	} `json:"checks"`
}

// batchRecorder answers every batch-check allowed and records request bodies.
func batchRecorder(t *testing.T) (*httptest.Server, func() []batchCheckBody) {
	t.Helper()
	var mu sync.Mutex
	var bodies []batchCheckBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var b batchCheckBody
		_ = json.Unmarshal(data, &b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		res := map[string]any{}
		for _, c := range b.Checks {
			res[c.CorrelationID] = map[string]any{"allowed": true}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": res})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []batchCheckBody {
		mu.Lock()
		defer mu.Unlock()
		return append([]batchCheckBody(nil), bodies...)
	}
}

func runBatch(t *testing.T, url, stdin string, args ...string) (string, error) {
	t.Helper()
	configtest.Isolate(t)
	cmd := New(newQueryCLI(t, url)).batchCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func writeBatchFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const batchJSON = `[{"user":"user:a","relation":"viewer","object":"doc:1","correlation_id":"first"},{"user":"user:b","relation":"viewer","object":"doc:2"}]`

func checkIDs(t *testing.T, bodies []batchCheckBody, want ...string) {
	t.Helper()
	if len(bodies) != 1 || len(bodies[0].Checks) != len(want) {
		t.Fatalf("bodies = %+v, want one request with %d checks", bodies, len(want))
	}
	for i, w := range want {
		if got := bodies[0].Checks[i].CorrelationID; got != w {
			t.Errorf("check %d id = %q, want %q", i, got, w)
		}
	}
}

func TestBatchCheckFileJSON(t *testing.T) {
	srv, bodies := batchRecorder(t)
	out, err := runBatch(t, srv.URL, "", "--file", writeBatchFile(t, "c.json", batchJSON))
	if err != nil {
		t.Fatal(err)
	}
	checkIDs(t, bodies(), "first", "c0")
	if !strings.Contains(out, "user:a viewer doc:1") || !strings.Contains(out, "user:b viewer doc:2") {
		t.Errorf("output = %q", out)
	}
}

func TestBatchCheckFileJSONL(t *testing.T) {
	srv, bodies := batchRecorder(t)
	content := `{"user":"user:a","relation":"viewer","object":"doc:1","correlation_id":"c0"}` + "\n\n" +
		`{"user":"user:b","relation":"viewer","object":"doc:2"}` + "\n"
	if _, err := runBatch(t, srv.URL, "", "--file", writeBatchFile(t, "c.jsonl", content)); err != nil {
		t.Fatal(err)
	}
	// The generated ID skips the file's own "c0".
	checkIDs(t, bodies(), "c0", "c1")
}

func TestBatchCheckFileStdin(t *testing.T) {
	for name, in := range map[string]string{
		"array": batchJSON,
		"jsonl": `{"user":"user:a","relation":"viewer","object":"doc:1","correlation_id":"first"}` + "\n" +
			`{"user":"user:b","relation":"viewer","object":"doc:2"}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			srv, bodies := batchRecorder(t)
			if _, err := runBatch(t, srv.URL, in, "--file", "-"); err != nil {
				t.Fatal(err)
			}
			checkIDs(t, bodies(), "first", "c0")
		})
	}
}

func TestBatchCheckFileAndCheckFlagsCombine(t *testing.T) {
	srv, bodies := batchRecorder(t)
	_, err := runBatch(t, srv.URL, "", "--file", writeBatchFile(t, "c.json", batchJSON), "--check", "user:z,editor,doc:9")
	if err != nil {
		t.Fatal(err)
	}
	b := bodies()
	checkIDs(t, b, "first", "c0", "c1")
	if got := b[0].Checks[2].TupleKey["user"]; got != "user:z" {
		t.Errorf("third check user = %v, want the --check item last", got)
	}
}

func TestBatchCheckFileValidationDetails(t *testing.T) {
	srv, bodies := batchRecorder(t)
	file := writeBatchFile(t, "c.json", `[{"user":"","relation":"viewer","object":"doc:1"},{"user":"user:a","relation":"viewer","object":"doc"}]`)
	_, err := runBatch(t, srv.URL, "", "--file", file)
	if err == nil {
		t.Fatal("want a validation error")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, clierr.CodeUsage)
	}
	env := clierr.NewEnvelope(err).Error
	if env.Code != clierr.KindValidation {
		t.Errorf("code = %q", env.Code)
	}
	if len(env.Details) != 2 {
		t.Fatalf("details = %v", env.Details)
	}
	for i, field := range []string{"user", "object"} {
		d := env.Details[i]
		if d["index"] != i || d["field"] != field {
			t.Errorf("detail %d = %v", i, d)
		}
	}
	if !strings.Contains(err.Error(), "entry 0: user") || !strings.Contains(err.Error(), "(and 1 more)") {
		t.Errorf("message = %q", err.Error())
	}
	if len(bodies()) != 0 {
		t.Error("no request may be sent")
	}
}

func TestBatchCheckFileDuplicateCorrelationID(t *testing.T) {
	srv, bodies := batchRecorder(t)
	file := writeBatchFile(t, "c.json", `[{"user":"user:a","relation":"v","object":"doc:1","correlation_id":"x"},{"user":"user:b","relation":"v","object":"doc:2","correlation_id":"x"}]`)
	_, err := runBatch(t, srv.URL, "", "--file", file)
	env := clierr.NewEnvelope(err).Error
	if env.Code != clierr.KindValidation || len(env.Details) != 1 || env.Details[0]["field"] != "correlation_id" || env.Details[0]["index"] != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	if len(bodies()) != 0 {
		t.Error("no request may be sent")
	}
}

func TestBatchCheckFileUnknownFieldRejected(t *testing.T) {
	srv, _ := batchRecorder(t)
	_, err := runBatch(t, srv.URL, "", "--file", writeBatchFile(t, "c.json", `[{"user":"user:a","relation":"v","object":"doc:1","bogus":1}]`))
	if err == nil || clierr.Code(err) != clierr.CodeUsage || clierr.NewEnvelope(err).Error.Code != clierr.KindValidation {
		t.Fatalf("err = %v", err)
	}
}

func TestBatchCheckFileContextualTuples(t *testing.T) {
	srv, bodies := batchRecorder(t)
	file := writeBatchFile(t, "c.json", `[{"user":"user:a","relation":"viewer","object":"doc:1","contextual_tuples":[{"user":"user:a","relation":"editor","object":"doc:1"}]},{"user":"user:b","relation":"viewer","object":"doc:2"}]`)
	if _, err := runBatch(t, srv.URL, "", "--file", file, "--contextual-tuple", "user:g,member,team:x"); err != nil {
		t.Fatal(err)
	}
	b := bodies()[0].Checks
	if n := len(b[0].ContextualTuples.TupleKeys); n != 2 {
		t.Fatalf("item 0 contextual tuples = %d, want global + own", n)
	}
	if b[0].ContextualTuples.TupleKeys[1]["relation"] != "editor" {
		t.Errorf("own tuple missing: %v", b[0].ContextualTuples.TupleKeys)
	}
	if n := len(b[1].ContextualTuples.TupleKeys); n != 1 {
		t.Errorf("item 1 contextual tuples = %d, want only the global one", n)
	}
}

func TestBatchCheckFileBadContextualTuple(t *testing.T) {
	srv, _ := batchRecorder(t)
	file := writeBatchFile(t, "c.json", `[{"user":"user:a","relation":"viewer","object":"doc:1","contextual_tuples":[{"user":"nope","relation":"editor","object":"doc:1"}]}]`)
	_, err := runBatch(t, srv.URL, "", "--file", file)
	env := clierr.NewEnvelope(err).Error
	if len(env.Details) != 1 || env.Details[0]["field"] != "contextual_tuples" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestBatchCheckNeedsAChecks(t *testing.T) {
	_, err := runBatch(t, "http://127.0.0.1:1", "")
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("code = %d (err %v), want usage", got, err)
	}
}
