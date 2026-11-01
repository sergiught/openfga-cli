package bench

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const wsModel = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user, user with cond]\n    define viewer: [user] or owner\n\ncondition cond(x: int) {\n  x > 1\n}\n"

func writeWS(t *testing.T, files map[string]string) *modeltest.Workspace {
	t.Helper()
	dir := t.TempDir()
	files["ofga.yaml"] = "version: 1\nmodel: ./model.fga\ntuples:\n  - \"tuples.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	files["model.fga"] = wsModel
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestWorkspaceSource_DedupesTuples(t *testing.T) {
	ws := writeWS(t, map[string]string{
		"tuples.yaml": "- user:anne owner document:1\n",
		"tests/a.test.yaml": "fixtures: [tuples]\ntests:\n" +
			"  - name: t1\n    tuples:\n      - user:bob owner document:1\n      - {user: user:cat, relation: owner, object: document:1, condition: {name: cond}}\n    check:\n      - {user: user:anne, object: document:1, assertions: {viewer: true}}\n" +
			"  - name: t2\n    tuples:\n      - user:bob owner document:1\n      - user:cat owner document:1\n    check:\n      - {user: user:bob, object: document:1, assertions: {viewer: true}}\n",
	})
	src, notices, err := FromWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != "workspace" {
		t.Fatalf("kind = %q", src.Kind)
	}
	var got []string
	for _, tp := range src.Tuples {
		got = append(got, tp.GetUser()+" "+tp.GetObject())
	}
	want := []string{"user:anne document:1", "user:bob document:1", "user:cat document:1"}
	if !slices.Equal(got, want) {
		t.Fatalf("tuples = %v, want %v", got, want)
	}
	if src.Tuples[2].GetCondition().GetName() != "cond" {
		t.Fatalf("first-wins violated: %v", src.Tuples[2])
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "user:cat owner document:1") || !strings.Contains(notices[0], "a/t2") {
		t.Fatalf("notices = %v", notices)
	}
}

func TestWorkspaceSource_SkipsInlineModelTests(t *testing.T) {
	ws := writeWS(t, map[string]string{
		"tuples.yaml":       "- user:anne owner document:1\n",
		"other.fga":         wsModel,
		"tests/a.test.yaml": "tests:\n  - name: t1\n    check:\n      - {user: user:anne, object: document:1, assertions: {viewer: true}}\n",
		"tests/b.test.yaml": "model: ../other.fga\ntests:\n  - name: t2\n    check:\n      - {user: user:anne, object: document:1, assertions: {owner: true}}\n",
		"tests/c.test.yaml": "model: ../model.fga\ntests:\n  - name: t3\n    check:\n      - {user: user:anne, object: document:1, assertions: {owner: true}}\n",
	})
	src, notices, err := FromWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Checks) != 2 {
		t.Fatalf("checks = %+v", src.Checks)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "b") {
		t.Fatalf("notices = %v", notices)
	}
	if !slices.Equal(src.Skipped, notices) {
		t.Fatalf("Skipped = %v", src.Skipped)
	}
}

func TestWorkspaceSource_OneCheckPerAssertion(t *testing.T) {
	ws := writeWS(t, map[string]string{
		"tuples.yaml": "- user:anne owner document:1\n",
		"tests/a.test.yaml": "tests:\n  - name: t1\n    check:\n" +
			"      - {user: user:anne, objects: [document:1, document:2], assertions: {viewer: true, owner: false}}\n",
	})
	src, _, err := FromWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []Check{
		{"a/t1 user:anne owner document:1", "user:anne", "owner", "document:1"},
		{"a/t1 user:anne viewer document:1", "user:anne", "viewer", "document:1"},
		{"a/t1 user:anne owner document:2", "user:anne", "owner", "document:2"},
		{"a/t1 user:anne viewer document:2", "user:anne", "viewer", "document:2"},
	}
	if !slices.Equal(src.Checks, want) {
		t.Fatalf("checks = %+v\nwant %+v", src.Checks, want)
	}
}
