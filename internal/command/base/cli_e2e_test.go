package base

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// sharedBin builds the ofga binary once for the whole package's e2e tests.
var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func ofgaBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ofga-e2e")
		if err != nil {
			binErr = err
			return
		}
		binPath = dir + "/ofga"
		out, err := exec.Command("go", "build", "-o", binPath, "github.com/sergiught/openfga-cli/cmd/ofga").CombinedOutput()
		if err != nil {
			binErr = err
			t.Logf("build output:\n%s", out)
		}
	})
	if binErr != nil {
		t.Fatalf("build ofga: %v", binErr)
	}
	return binPath
}

// runOfga runs the binary with an isolated config dir. extraEnv is appended to a
// clean environment; args are the command. It returns stdout, stderr and the
// exit code.
func runOfga(t *testing.T, cfgHome string, stdin string, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(ofgaBin(t), args...)
	cmd.Env = append([]string{"XDG_CONFIG_HOME=" + cfgHome, "NO_COLOR=1", "PATH=" + os.Getenv("PATH")}, extraEnv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run ofga %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

func TestProfilesLifecycle(t *testing.T) {
	home := t.TempDir()

	// add via --token-stdin: secret must not appear in argv or in output.
	out, _, code := runOfga(t, home, "s3cr3t-token\n", nil,
		"profiles", "add", "prod", "--api-url", "https://fga.example.com", "--token-stdin")
	if code != 0 {
		t.Fatalf("add exit=%d out=%q", code, out)
	}

	// show: token is masked, never leaked.
	out, _, _ = runOfga(t, home, "", nil, "profiles", "show", "prod")
	if strings.Contains(out, "s3cr3t-token") {
		t.Errorf("profiles show leaked the token:\n%s", out)
	}
	if !strings.Contains(out, "•") {
		t.Errorf("token should be masked with bullets:\n%s", out)
	}

	// list --json: snake_case keys, secret hidden.
	out, _, _ = runOfga(t, home, "", nil, "profiles", "list", "--json")
	if !strings.Contains(out, "api_url") {
		t.Errorf("expected snake_case api_url in JSON:\n%s", out)
	}
	if strings.Contains(out, "s3cr3t-token") {
		t.Errorf("JSON leaked the token:\n%s", out)
	}

	// remove without --force, non-interactive: refuses, exit 1.
	_, errb, code := runOfga(t, home, "", nil, "profiles", "remove", "prod")
	if code == 0 || !strings.Contains(errb, "--force") {
		t.Errorf("remove should refuse without --force: code=%d err=%q", code, errb)
	}

	// remove --force: succeeds.
	_, _, code = runOfga(t, home, "", nil, "profiles", "remove", "prod", "--force")
	if code != 0 {
		t.Errorf("remove --force should succeed, code=%d", code)
	}
}

func TestDidYouMeanAndAliases(t *testing.T) {
	home := t.TempDir()

	// singular alias resolves.
	_, _, code := runOfga(t, home, "", nil, "store", "--help")
	if code != 0 {
		t.Errorf("`ofga store --help` (singular alias) should work, code=%d", code)
	}

	// typo yields a suggestion.
	_, errb, code := runOfga(t, home, "", nil, "stroes")
	if code == 0 || !strings.Contains(errb, "Did you mean") {
		t.Errorf("typo should suggest a command: code=%d err=%q", code, errb)
	}
}

func TestUsageErrorVsRuntimeError(t *testing.T) {
	home := t.TempDir()

	// Bad invocation (too many args): exit 2 (CodeUsage), diagnostic and hint on
	// stderr, nothing leaked to stdout for a script capturing it.
	out, errb, code := runOfga(t, home, "", nil, "query", "check", "a", "b", "c", "d")
	if code != 2 {
		t.Errorf("arg error should exit 2 (CodeUsage), got %d (err=%q)", code, errb)
	}
	if out != "" {
		t.Errorf("usage error should not write to stdout, got %q", out)
	}
	if !strings.Contains(errb, "--help") {
		t.Errorf("usage error should hint at --help on stderr, got %q", errb)
	}

	// runtime (network) error: no usage hint, friendly message, exit 4.
	env := []string{"OPENFGA_API_URL=http://127.0.0.1:0", "OPENFGA_STORE_ID=01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	out, errb, code = runOfga(t, home, "", env, "stores", "list")
	if code != 4 {
		t.Errorf("network error should exit 4, got %d (err=%q)", code, errb)
	}
	if strings.Contains(out+errb, "--help for usage") {
		t.Errorf("runtime error should not print a usage hint:\nout=%s\nerr=%s", out, errb)
	}
	if !strings.Contains(errb, "cannot reach") {
		t.Errorf("network error should be humanized:\n%s", errb)
	}
}

func TestDryRunAndValidation(t *testing.T) {
	home := t.TempDir()
	store := []string{"--store-id", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--api-url", "http://127.0.0.1:0"}

	// dry-run writes nothing and needs no server. The preview is a status line,
	// so it goes to stderr (stdout stays a clean data stream).
	out, errb, code := runOfga(t, home, "", nil,
		append([]string{"tuples", "write", "user:anne", "viewer", "doc:1", "--dry-run"}, store...)...)
	if code != 0 || !strings.Contains(errb, "would write") || out != "" {
		t.Errorf("dry-run should preview on stderr without calling the API: code=%d out=%q err=%q", code, out, errb)
	}

	// swapped/invalid tuple is rejected locally (before any network call).
	_, errb, code = runOfga(t, home, "", nil,
		append([]string{"tuples", "write", "anne", "viewer", "doc:1", "--dry-run"}, store...)...)
	if code == 0 || !strings.Contains(errb, "type:id") {
		t.Errorf("invalid user should be rejected locally: code=%d err=%q", code, errb)
	}
}

func TestConfigPathAndInit(t *testing.T) {
	home := t.TempDir()

	out, _, code := runOfga(t, home, "", nil, "config", "path")
	if code != 0 || !strings.Contains(out, "config.toml") {
		t.Errorf("config path should print the file path: code=%d out=%q", code, out)
	}

	// init on a fresh config configures the default profile without prompting.
	_, _, code = runOfga(t, home, "", nil, "init", "--api-url", "http://localhost:8080")
	if code != 0 {
		t.Fatalf("init should succeed on fresh config, code=%d", code)
	}
	out, _, _ = runOfga(t, home, "", nil, "profiles", "show")
	if !strings.Contains(out, "http://localhost:8080") {
		t.Errorf("init should have saved the API URL:\n%s", out)
	}
}

// TestStoresListAgainstMockServer exercises the full client path against a
// stand-in OpenFGA HTTP endpoint.
func TestStoresListAgainstMockServer(t *testing.T) {
	const hostileName = "demo\x1b]52;c;clipboard\a\nforged"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/stores" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stores": []map[string]any{
					{"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": hostileName,
						"created_at": "2023-01-01T00:00:00Z", "updated_at": "2023-01-01T00:00:00Z"},
				},
				"continuation_token": "",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	home := t.TempDir()
	env := []string{"OPENFGA_API_URL=" + srv.URL}

	out, errb, code := runOfga(t, home, "", env, "stores", "list")
	if code != 0 {
		t.Fatalf("stores list exit=%d err=%q", code, errb)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("expected the mock store in output:\n%s", out)
	}
	if strings.ContainsAny(out, "\x1b\a") || strings.Contains(out, "\nforged") {
		t.Errorf("human output retained terminal controls from the server: %q", out)
	}
	if strings.Contains(out, "─") {
		t.Errorf("piped table retained box-drawing: %q", out)
	}

	// --json path returns the store as machine-readable output.
	out, _, code = runOfga(t, home, "", env, "stores", "list", "--json")
	if code != 0 || !strings.Contains(out, `\u001b`) {
		t.Errorf("stores list --json should emit the store: code=%d out=%q", code, out)
	}
}

func TestGlobalStructuredOutputContracts(t *testing.T) {
	home := t.TempDir()

	out, errb, code := runOfga(t, home, "", nil, "config", "path", "--json")
	if code != 0 {
		t.Fatalf("config path --json exit=%d stderr=%q", code, errb)
	}
	var pathResult map[string]string
	if err := json.Unmarshal([]byte(out), &pathResult); err != nil || pathResult["path"] == "" {
		t.Fatalf("config path --json returned invalid shape: out=%q err=%v", out, err)
	}

	out, errb, code = runOfga(t, home, "", nil, "theme", "--yaml")
	if code != 0 {
		t.Fatalf("theme --yaml exit=%d stderr=%q", code, errb)
	}
	if !strings.Contains(out, "available:") || !strings.Contains(out, "current:") {
		t.Fatalf("theme --yaml returned human output: %q", out)
	}
}
