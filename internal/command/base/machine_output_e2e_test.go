package base

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

const e2eStoreID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// Agent mode implies --no-input, so a destructive command without --force
// must refuse with the confirmation_required envelope before it reaches the
// server.
func TestAgentModeDestructiveRequiresForce(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	api := []string{"OPENFGA_API_URL=" + srv.URL, "OPENFGA_STORE_ID=" + e2eStoreID}

	tests := []struct {
		name string
		env  []string
		args []string
	}{
		{"tuples delete, OPENFGA_AGENT_MODE=1", append([]string{"OPENFGA_AGENT_MODE=1"}, api...),
			[]string{"tuples", "delete", "user:anne", "viewer", "document:x"}},
		{"tuples delete, --agent", api,
			[]string{"--agent", "tuples", "delete", "user:anne", "viewer", "document:x"}},
		{"stores delete, CLAUDECODE", append([]string{"CLAUDECODE=1"}, api...),
			[]string{"stores", "delete", e2eStoreID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errb, code := runOfga(t, t.TempDir(), "", tt.env, tt.args...)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (stdout=%q stderr=%q)", code, out, errb)
			}
			lines := nonEmptyLines(errb)
			if len(lines) != 1 {
				t.Fatalf("stderr has %d non-empty lines, want 1: %q", len(lines), errb)
			}
			var env clierr.Envelope
			if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
				t.Fatalf("stderr is not an envelope: %v (%q)", err, lines[0])
			}
			if env.Error.Code != clierr.KindConfirmationRequired {
				t.Errorf("code = %q, want %q", env.Error.Code, clierr.KindConfirmationRequired)
			}
			if !strings.Contains(env.Error.Hint, "--force") {
				t.Errorf("hint = %q, want --force", env.Error.Hint)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("server was called %d time(s), want 0", n)
			}
		})
	}
}

// A canceled command under --json reports a single "canceled" envelope line
// on stderr and exits 130, the same graceful path as Ctrl-C.
func TestCanceledJSONEnvelope(t *testing.T) {
	gotRequest := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotRequest <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	env := []string{
		"OPENFGA_CONFIG=" + filepath.Join(t.TempDir(), "config.toml"),
		"NO_COLOR=1", "PATH=" + os.Getenv("PATH"),
		"OPENFGA_AGENT_MODE=0",
		"OPENFGA_API_URL=" + srv.URL,
	}
	for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	cmd := exec.Command(ofgaBin(t), "stores", "list", "--json")
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ofga: %v", err)
	}
	select {
	case <-gotRequest:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("ofga never reached the mock server")
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal ofga: %v", err)
	}
	waitErr := cmd.Wait()
	code := 0
	if exit, ok := waitErr.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if waitErr != nil {
		t.Fatalf("wait ofga: %v", waitErr)
	}
	if code != clierr.CodeCanceled {
		t.Errorf("exit = %d, want %d; stderr=%q", code, clierr.CodeCanceled, stderr.String())
	}
	lines := nonEmptyLines(stderr.String())
	if len(lines) != 1 {
		t.Fatalf("stderr has %d non-empty lines, want 1: %q", len(lines), stderr.String())
	}
	var e clierr.Envelope
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("stderr is not an envelope: %v (%q)", err, lines[0])
	}
	if e.Error.Code != clierr.KindCanceled {
		t.Errorf("code = %q, want %q", e.Error.Code, clierr.KindCanceled)
	}
}

// List-style commands that get an empty collection back must still produce
// valid YAML and valid --jq output, never a panic.
func TestEmptyResultsYAMLAndJQ(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case p == "/stores":
			_, _ = w.Write([]byte(`{"stores":[]}`))
		case strings.HasSuffix(p, "/authorization-models"):
			_, _ = w.Write([]byte(`{"authorization_models":[]}`))
		case strings.HasSuffix(p, "/read"):
			_, _ = w.Write([]byte(`{"tuples":[]}`))
		case strings.HasSuffix(p, "/changes"):
			_, _ = w.Write([]byte(`{"changes":[]}`))
		case strings.HasSuffix(p, "/list-objects"):
			_, _ = w.Write([]byte(`{"objects":[]}`))
		case strings.HasSuffix(p, "/list-users"):
			_, _ = w.Write([]byte(`{"users":[]}`))
		case strings.Contains(p, "/assertions/"):
			_, _ = w.Write([]byte(`{"assertions":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"unexpected","message":"unexpected request"}`))
		}
	}))
	defer srv.Close()
	env := []string{"OPENFGA_API_URL=" + srv.URL, "OPENFGA_STORE_ID=" + e2eStoreID, "OPENFGA_AGENT_MODE=0"}

	cmds := [][]string{
		{"stores", "list"},
		{"model", "list"},
		{"tuples", "read"},
		{"tuples", "changes"},
		{"query", "list-objects", "document", "viewer", "user:anne"},
		{"query", "list-users", "document:x", "viewer", "--type", "user"},
		{"assertions", "read", "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	for _, args := range cmds {
		name := strings.Join(args, " ")
		t.Run(name+" --yaml", func(t *testing.T) {
			out, errb, code := runOfga(t, t.TempDir(), "", env, append(args[:len(args):len(args)], "--yaml")...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr=%q", code, errb)
			}
			if strings.Contains(errb, "panic") {
				t.Fatalf("panic: %q", errb)
			}
			var v any
			if err := yaml.Unmarshal([]byte(out), &v); err != nil {
				t.Fatalf("stdout is not valid YAML: %v (%q)", err, out)
			}
		})
		t.Run(name+" --jq .", func(t *testing.T) {
			out, errb, code := runOfga(t, t.TempDir(), "", env, append(args[:len(args):len(args)], "--jq", ".")...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr=%q", code, errb)
			}
			if strings.Contains(errb, "panic") {
				t.Fatalf("panic: %q", errb)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatalf("empty --jq output")
			}
			dec := json.NewDecoder(strings.NewReader(out))
			for dec.More() {
				var v any
				if err := dec.Decode(&v); err != nil {
					t.Fatalf("--jq output is not valid JSON: %v (%q)", err, out)
				}
			}
		})
	}
}
