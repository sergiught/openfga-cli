package mapping

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/output"
)

const validMapping = `# Org membership sync. Owned by the identity team.
version: "1"
rules:
  # Someone joined.
  - name: member-added
    tuples: # one per membership
      - object: 'organization:{{ input.data.object.organization.id }}'
        relation: member
        user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
    when: input.type == "organization.member.added"
  - name: member-removed
    when: input.type == "organization.member.deleted"
    action: delete
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: member
        object: "organization:{{ input.data.object.organization.id }}"
tests:
  - name: added
    input: {type: organization.member.added, data: {object: {organization: {id: o1}, user: {user_id: u1}}}}
    expect_tuples:
      - user: "user:u1"
        relation: member
        object: "organization:o1"
        action: write
# end of file
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateValidFile(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p)
	if err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if !strings.Contains(out, "2 rules, 1 test;") || !strings.Contains(out, "model not checked (pass --model-file or --model-from-store)") {
		t.Fatalf("out = %s", out)
	}
}

func TestValidateModelGapFails(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	m := writeFile(t, dir, "model.fga", "model\n  schema 1.1\n\ntype user\n")
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p, "--model-file", m)
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, "mapping.yaml:5:") || !strings.Contains(out, "organization") {
		t.Fatalf("code=%d out=%s", clierr.Code(err), out)
	}
}

func TestValidateModelFileSuccessLine(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	m := writeFile(t, dir, "model.fga", "model\n  schema 1.1\n\ntype user\n\ntype organization\n  relations\n    define member: [user]\n")
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p, "--model-file", m)
	if err != nil || !strings.Contains(out, "checked against "+m+" (2 types)") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestValidateRefusesAModelFileWithNoTypes(t *testing.T) {
	configtest.Isolate(t)
	for _, model := range []string{`{}`, `{"schema_version":"1.1","type_definitions":[]}`} {
		dir := t.TempDir()
		p := writeFile(t, dir, "mapping.yaml", validMapping)
		m := writeFile(t, dir, "model.json", model)
		_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p, "--model-file", m)
		if err == nil || !strings.Contains(err.Error(), "defines no types") {
			t.Errorf("%s: err = %v", model, err)
		}
	}
}

func TestValidateFlagsAreExclusive(t *testing.T) {
	configtest.Isolate(t)
	_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", "x.yaml", "--model-file", "m.fga", "--model-from-store")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d", clierr.Code(err))
	}
}

func TestValidateJSON(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", "version: \"1\"\nrules:\n  - name: r\n    when: \"input.type ==\"\n    tuples:\n      - user: \"user:1\"\n        relation: m\n        object: \"t:1\"\n")
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.JSON = true
	out, err := run(t, c, "validate", p)
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, `"valid": false`) || !strings.Contains(out, `"line": 4`) {
		t.Fatalf("code=%d out=%s", clierr.Code(err), out)
	}
}

func TestValidateSyntaxErrorHasNoPositionEcho(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", "version: \"1\"\nrules: [\n")
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p)
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, "mapping.yaml:") || strings.Contains(out, ": [") {
		t.Fatalf("code=%d out=%s", clierr.Code(err), out)
	}
}

func TestValidateFromStoreUsesModelID(t *testing.T) {
	configtest.Isolate(t)
	var mu sync.Mutex
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authorization_model":{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","schema_version":"1.1","type_definitions":[{"type":"user"}]}}`)
	}))
	defer srv.Close()
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: srv.URL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ModelID: "01ARZ3NDEKTSV4RRFFQ69G5FAW"})
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	out, err := run(t, cli.New(log.New(io.Discard), cfg, "test"), "validate", p, "--model-from-store")
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(path, "01ARZ3NDEKTSV4RRFFQ69G5FAW") || clierr.Code(err) != clierr.CodeError || !strings.Contains(out, "organization") {
		t.Fatalf("path=%q err=%v out=%s", path, err, out)
	}
}

func TestValidateMissingFiles(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	c := cli.New(log.New(io.Discard), config.New(), "test")
	_, err := run(t, c, "validate", filepath.Join(dir, "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "nope.yaml not found") || !strings.Contains(err.Error(), "create one with 'ofga mappings") || clierr.Code(err) != clierr.CodeError {
		t.Fatalf("err = %v", err)
	}
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	_, err = run(t, c, "validate", p, "--model-file", filepath.Join(dir, "m.fga"))
	if err == nil || !strings.Contains(err.Error(), "model file") || !strings.Contains(err.Error(), "m.fga not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatePlainShowsProblems(t *testing.T) {
	configtest.Isolate(t)
	output.Plain = true
	t.Cleanup(func() { output.Plain = false })
	p := writeFile(t, t.TempDir(), "mapping.yaml", "version: \"1\"\nrules: [\n")
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p)
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, "mapping.yaml:2:8: ") || !strings.Contains(out, "problems\t1") {
		t.Fatalf("code=%d out=%s", clierr.Code(err), out)
	}
}

func TestValidateSingularCounts(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	one := "version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"organization:1\"\ntests:\n  - name: t\n    input: {}\n    expect_tuples: []\n"
	p := writeFile(t, dir, "mapping.yaml", one)
	m := writeFile(t, dir, "model.fga", "model\n  schema 1.1\n\ntype user\n")
	c := cli.New(log.New(io.Discard), config.New(), "test")
	out, err := run(t, c, "validate", p, "--model-file", m)
	if err == nil || !strings.Contains(out, "1 problem\n") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	full := writeFile(t, dir, "full.fga", "model\n  schema 1.1\n\ntype user\n\ntype organization\n  relations\n    define member: [user]\n")
	out, err = run(t, c, "validate", p, "--model-file", full)
	if err != nil || !strings.Contains(out, "1 rule, 1 test;") || !strings.Contains(out, "(2 types)") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestValidateFlagsATypoInATupleFilter(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", `version: "1"
rules:
  - name: removed
    when: input.type == "removed"
    tuple_filters:
      - user: "user:{{ input.user }}"
        relation: memebr
        object: "organization:"
        action: delete
`)
	m := writeFile(t, dir, "model.fga", "model\n  schema 1.1\n\ntype user\n\ntype organization\n  relations\n    define member: [user]\n")
	out, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p, "--model-file", m)
	want := p + `:3:5: rules["removed"].tuple_filters: type "organization" has no relation "memebr"`
	if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, want) {
		t.Fatalf("code=%d out=%s", clierr.Code(err), out)
	}
}

func TestValidateRejectsAModularModel(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "mapping.yaml", validMapping)
	m := writeFile(t, dir, "fga.mod", "schema: '1.2'\ncontents:\n  - core.fga\n")
	_, err := run(t, cli.New(log.New(io.Discard), config.New(), "test"), "validate", p, "--model-file", m)
	if err == nil || err.Error() != "modular models (fga.mod) are not supported yet; pass the model's .fga or .json file" {
		t.Fatalf("err = %v", err)
	}
}
