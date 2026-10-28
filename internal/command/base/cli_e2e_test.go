package base

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// sharedBin builds the ofga binary once for the whole package's e2e tests.
var (
	binOnce sync.Once
	binDir  string
	binPath string
	binErr  error
)

// TestMain removes the shared binary's directory once the package's tests are
// done; it outlives any one test, so t.TempDir() can't own it.
func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func ofgaBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ofga-e2e")
		if err != nil {
			binErr = err
			return
		}
		binDir = dir
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

// runOfga runs the binary with an isolated config file inside cfgHome. extraEnv
// is appended to a clean environment; args are the command. It returns stdout,
// stderr and the exit code.
func runOfga(t *testing.T, cfgHome string, stdin string, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(ofgaBin(t), args...)
	// A clean base env keeps the developer's real OpenFGA config and auth out of
	// the test. OPENFGA_CONFIG pins the config file directly rather than through
	// XDG_CONFIG_HOME, which macOS ignores — there the default lives under
	// ~/Library/Application Support, so an XDG-only child would write the
	// developer's real config.
	//
	// The CLI stores secrets in the OS keyring, so forward the vars that let the
	// child reach it: the session D-Bus on Linux, and HOME on macOS, where
	// `security` resolves the login keychain through it. Without them
	// secretsAvailable() reports the keyring missing and every secret write
	// fails (fast locally, after a long dbus autolaunch stall in CI).
	env := []string{
		"OPENFGA_CONFIG=" + filepath.Join(cfgHome, "config.toml"),
		"NO_COLOR=1",
		"PATH=" + os.Getenv("PATH"),
	}
	for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "HOME"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	cmd.Env = append(env, extraEnv...)
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
	out, errb, code := runOfga(t, home, "s3cr3t-token\n", nil,
		"profiles", "add", "prod", "--api-url", "https://fga.example.com", "--token-stdin")
	if code != 0 {
		// This test writes a real secret, so it needs a writable OS keyring. CI
		// provisions one and sets OFGA_E2E_REQUIRE_KEYRING so a broken setup
		// fails loudly; elsewhere, skip rather than fail (and rather than write
		// to the developer's real keyring) when none is available or unlocked.
		if os.Getenv("OFGA_E2E_REQUIRE_KEYRING") == "" &&
			(strings.Contains(errb, "keyring is unavailable") || strings.Contains(errb, "locked collection")) {
			t.Skipf("no writable OS keyring: %s", strings.TrimSpace(errb))
		}
		t.Fatalf("add exit=%d out=%q err=%q", code, out, errb)
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
	_, errb, code = runOfga(t, home, "", nil, "profiles", "remove", "prod")
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
	out, errb, code = runOfga(t, home, "", nil,
		append([]string{"tuples", "write", "user:anne", "viewer", "doc:1", "--dry-run", "--plain"}, store...)...)
	if code != 0 || errb != "" || out != "dry_run\ttrue\nwould_write\t1\n" {
		t.Errorf("plain dry-run should emit stable TSV: code=%d out=%q err=%q", code, out, errb)
	}

	// swapped/invalid tuple is rejected locally (before any network call).
	_, errb, code = runOfga(t, home, "", nil,
		append([]string{"tuples", "write", "anne", "viewer", "doc:1", "--dry-run"}, store...)...)
	if code == 0 || !strings.Contains(errb, "type:id") {
		t.Errorf("invalid user should be rejected locally: code=%d err=%q", code, errb)
	}
}

func TestNonInteractiveRootAndPlayground(t *testing.T) {
	home := t.TempDir()
	out, errb, code := runOfga(t, home, "", nil)
	if code != 0 || errb != "" || !strings.Contains(out, "Run \"ofga --help\"") {
		t.Fatalf("bare non-TTY invocation should print concise guidance: code=%d out=%q err=%q", code, out, errb)
	}
	if strings.Contains(out, "Environment:") {
		t.Fatalf("bare non-TTY invocation printed the full help reference: %q", out)
	}

	out, errb, code = runOfga(t, home, "", nil, "playground")
	if code != 2 || out != "" || !strings.Contains(errb, "requires an interactive terminal") {
		t.Fatalf("explicit non-TTY playground should fail: code=%d out=%q err=%q", code, out, errb)
	}
}

func TestStructuredMutationResults(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		key  string
	}{
		{"init", []string{"init", "--no-input", "--json"}, "profile"},
		{"profile add", []string{"profiles", "add", "dev", "--json"}, "created"},
		{"theme", []string{"theme", "nord", "--json"}, "theme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runOfga(t, home, "", nil, tc.args...)
			if code != 0 {
				t.Fatalf("mutation failed: code=%d stderr=%q", code, errb)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("mutation output is not JSON: %q: %v", out, err)
			}
			if _, ok := got[tc.key]; !ok {
				t.Fatalf("mutation output missing %q: %v", tc.key, got)
			}
		})
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

// TestSIGTERMCancelsGracefully covers CLI-77: a kill/CI-runner cancellation
// (SIGTERM) must take the same graceful path as Ctrl-C (os.Interrupt) —
// "canceled" on stderr and exit code 130 — rather than an unhandled kill.
func TestSIGTERMCancelsGracefully(t *testing.T) {
	gotRequest := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotRequest <- struct{}{}:
		default:
		}
		<-r.Context().Done() // hang until the client cancels the in-flight request
	}))
	defer srv.Close()

	// OPENFGA_CONFIG rather than XDG_CONFIG_HOME, which macOS ignores — see
	// runOfga.
	env := []string{
		"OPENFGA_CONFIG=" + filepath.Join(t.TempDir(), "config.toml"),
		"NO_COLOR=1", "PATH=" + os.Getenv("PATH"),
		"OPENFGA_API_URL=" + srv.URL,
	}
	for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}

	cmd := exec.Command(ofgaBin(t), "stores", "list")
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

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal ofga: %v", err)
	}

	waitErr := cmd.Wait()
	code := 0
	if exit, ok := waitErr.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if waitErr != nil {
		t.Fatalf("wait ofga: %v", waitErr)
	}
	if code != 130 {
		t.Errorf("exit code = %d, want 130; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "canceled") {
		t.Errorf("stderr should report cancellation: %q", stderr.String())
	}
}

// writeCorruptConfig writes an unparseable config.toml where a runOfga child
// will look for it, and returns its path.
func writeCorruptConfig(t *testing.T, cfgHome string) string {
	t.Helper()
	// Must match the path runOfga pins OPENFGA_CONFIG to — cfgHome/config.toml,
	// not the XDG-style cfgHome/ofga/config.toml. Writing to the latter would
	// leave the child reading a non-existent config, quietly falling back to
	// defaults, and the test would assert a warning that never comes.
	path := filepath.Join(cfgHome, "config.toml")
	if err := os.WriteFile(path, []byte("this is = not [valid toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigLoadErrorWarnsOnEveryCommand covers CLI-75: a corrupt config file
// must be surfaced on stderr for ANY command, not just `profiles` and
// `config init`, while the command still proceeds on defaults (warn, not
// fatal). `config path` is used because it never touches the network and
// always succeeds, isolating the assertion to the warning itself.
func TestConfigLoadErrorWarnsOnEveryCommand(t *testing.T) {
	// The CLI reports the config path canonicalized, so the expectation has to
	// be canonical too. On macOS t.TempDir() sits under /var, itself a symlink
	// to /private/var, and the substring assertions below would never match.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := writeCorruptConfig(t, home)

	out, errb, code := runOfga(t, home, "", nil, "config", "path")
	if code != 0 {
		t.Fatalf("command should still proceed on a corrupt config: code=%d err=%q", code, errb)
	}
	if !strings.Contains(out, "config.toml") {
		t.Errorf("config path should still print the resolved path: %q", out)
	}
	if !strings.Contains(errb, path) {
		t.Errorf("warning should name the config path %q: stderr=%q", path, errb)
	}
	if !strings.Contains(errb, "parse config") {
		t.Errorf("warning should name the parse error: stderr=%q", errb)
	}

	// --quiet suppresses the warning entirely.
	out, errb, code = runOfga(t, home, "", nil, "config", "path", "--quiet")
	if code != 0 {
		t.Fatalf("--quiet run should still succeed: code=%d err=%q", code, errb)
	}
	if errb != "" {
		t.Errorf("--quiet should suppress the load-error warning, got stderr=%q", errb)
	}
	if !strings.Contains(out, "config.toml") {
		t.Errorf("--quiet should not suppress the command's own output: %q", out)
	}

	// Machine-output modes keep stdout clean; the warning still reaches stderr.
	out, errb, code = runOfga(t, home, "", nil, "config", "path", "--json")
	if code != 0 {
		t.Fatalf("--json run should still succeed: code=%d err=%q", code, errb)
	}
	var pathResult map[string]string
	if err := json.Unmarshal([]byte(out), &pathResult); err != nil || pathResult["path"] == "" {
		t.Fatalf("--json stdout must stay clean JSON: out=%q err=%v", out, err)
	}
	if !strings.Contains(errb, path) {
		t.Errorf("--json mode should still warn on stderr: stderr=%q", errb)
	}
}

func TestMachineErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/write"):
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "user:2") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"validation_error","message":"bad tuple"}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/stores/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"store_id_not_found","message":"store not found"}`))
		default:
			_, _ = w.Write([]byte(`{"stores":[]}`))
		}
	}))
	defer srv.Close()

	tuples := filepath.Join(t.TempDir(), "tuples.json")
	if err := os.WriteFile(tuples, []byte(`[
{"user":"user:1","relation":"viewer","object":"doc:1"},
{"user":"user:2","relation":"viewer","object":"doc:1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	badBatch := filepath.Join(t.TempDir(), "bad-batch.jsonl")
	if err := os.WriteFile(badBatch, []byte(`{"user":"user:a","relation":"viewer","object":"doc:1"}
{"user":"user:b","relation":"viewer"}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	badContextual := filepath.Join(t.TempDir(), "bad-contextual.json")
	if err := os.WriteFile(badContextual, []byte(`[{"user":"user:a","relation":"viewer","object":"doc:1"},{"user":"nope","relation":"viewer","object":"doc:1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	badCfg := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(badCfg, []byte("not = [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	const storeID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	api := []string{"OPENFGA_API_URL=" + srv.URL, "OPENFGA_STORE_ID=" + storeID}

	tests := []struct {
		name  string
		env   []string
		args  []string
		exit  int
		code  clierr.Kind
		check func(t *testing.T, out string, e clierr.EnvelopeBody)
	}{
		{name: "unknown command", args: []string{"stores", "lisst", "--json"}, exit: 2, code: clierr.KindUsage,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if len(e.Details) == 0 || e.Details[0]["suggestion"] != "list" {
					t.Errorf("details = %v, want suggestion list", e.Details)
				}
			}},
		{name: "unknown flag", args: []string{"stores", "list", "--bogus", "--json"}, exit: 2, code: clierr.KindUsage},
		{name: "arg count", args: []string{"query", "check", "user:anne", "viewer", "--json"}, exit: 2, code: clierr.KindUsage},
		{name: "network", env: []string{"OPENFGA_STORE_ID=" + storeID}, args: []string{"stores", "list", "--json", "--api-url", "http://127.0.0.1:1"}, exit: 4, code: clierr.KindNetwork},
		{name: "not found", env: api, args: []string{"stores", "get", "--store-id", storeID, "--json"}, exit: 1, code: clierr.KindNotFound,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if e.Status != 404 {
					t.Errorf("status = %d, want 404", e.Status)
				}
			}},
		{name: "needs force", env: api, args: []string{"stores", "delete", storeID, "--json"}, exit: 1, code: clierr.KindConfirmationRequired,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if !strings.Contains(e.Hint, "--force") {
					t.Errorf("hint = %q, want --force", e.Hint)
				}
			}},
		{name: "yaml output", args: []string{"stores", "lisst", "-o", "yaml"}, exit: 2, code: clierr.KindUsage},
		{name: "bad jq", env: api, args: []string{"stores", "list", "--jq", ".["}, exit: 2, code: clierr.KindUsage},
		{name: "partial failure", env: api, args: []string{"tuples", "write", "--file", tuples, "--max-tuples-per-write", "1", "--json"}, exit: 1, code: clierr.KindPartialFailure,
			check: func(t *testing.T, out string, _ clierr.EnvelopeBody) {
				if !json.Valid([]byte(out)) {
					t.Errorf("stdout is not valid JSON: %q", out)
				}
			}},
		{name: "partial failure quiet", env: api, args: []string{"tuples", "write", "--file", tuples, "--max-tuples-per-write", "1", "--json", "--quiet"}, exit: 1, code: clierr.KindPartialFailure,
			check: func(t *testing.T, out string, _ clierr.EnvelopeBody) {
				if !json.Valid([]byte(out)) {
					t.Errorf("stdout is not valid JSON: %q", out)
				}
			}},
		{name: "bad batch file", env: api, args: []string{"query", "batch-check", "--file", badBatch, "--json"}, exit: 2, code: clierr.KindValidation,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if len(e.Details) == 0 || e.Details[0]["index"] != float64(1) || e.Details[0]["field"] != "object" {
					t.Errorf("details = %v, want entry 1 object problem", e.Details)
				}
			}},
		{name: "bad contextual tuples file", env: api, args: []string{"query", "check", "user:anne", "viewer", "doc:1", "--contextual-tuples-file", badContextual, "--json"}, exit: 2, code: clierr.KindValidation,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if len(e.Details) == 0 || e.Details[0]["index"] != float64(1) || e.Details[0]["source"] != badContextual {
					t.Errorf("details = %v, want tuple index 1 for the file", e.Details)
				}
			}},
		{name: "config load", env: []string{"OPENFGA_CONFIG=" + badCfg}, args: []string{"stores", "list", "--json"}, exit: 1, code: clierr.KindError,
			check: func(t *testing.T, _ string, e clierr.EnvelopeBody) {
				if !strings.Contains(e.Hint, "fix or remove") {
					t.Errorf("hint = %q, want fix or remove", e.Hint)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errb, code := runOfga(t, t.TempDir(), "", tt.env, tt.args...)
			if code != tt.exit {
				t.Fatalf("exit = %d, want %d (stderr=%q)", code, tt.exit, errb)
			}
			var lines []string
			for _, l := range strings.Split(errb, "\n") {
				if strings.TrimSpace(l) != "" {
					lines = append(lines, l)
				}
			}
			// The config-load warning is informational and stays on stderr
			// ahead of the failure; every other row has the envelope alone.
			if tt.name == "config load" && len(lines) > 1 && strings.HasPrefix(lines[0], "● warning:") {
				lines = lines[1:]
			}
			if len(lines) != 1 {
				t.Fatalf("stderr has %d non-empty lines, want 1: %q", len(lines), errb)
			}
			var env clierr.Envelope
			if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
				t.Fatalf("stderr is not an envelope: %v (%q)", err, lines[0])
			}
			if env.Error.Code != tt.code {
				t.Errorf("code = %q, want %q (%+v)", env.Error.Code, tt.code, env.Error)
			}
			if env.Error.Details == nil {
				t.Error("details must be non-nil")
			}
			if tt.check != nil {
				tt.check(t, out, env.Error)
			}
		})
	}
}

func TestHumanErrorsUnchanged(t *testing.T) {
	_, errb, code := runOfga(t, t.TempDir(), "", nil, "stores", "lisst")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "●") || !strings.Contains(errb, "--help") {
		t.Errorf("human error lost its bullet or hint: %q", errb)
	}
	for _, l := range strings.Split(errb, "\n") {
		if json.Valid([]byte(strings.TrimSpace(l))) && strings.HasPrefix(strings.TrimSpace(l), "{") {
			t.Errorf("human stderr contains JSON: %q", l)
		}
	}
}

func TestCommandsWorksOffline(t *testing.T) {
	home := t.TempDir()
	out, errb, code := runOfga(t, home, "", nil, "commands", "--json")
	if code != 0 {
		t.Fatalf("commands failed: code=%d stderr=%q", code, errb)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("commands output is not valid JSON: %q", out)
	}
}
