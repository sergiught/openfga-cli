package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFindMappingsDepthAndSkips(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"top.yaml":                miniMapping,
		"b.yml":                   miniMapping,
		"one/two/three.yaml":      miniMapping,
		"one/two/three/four.yaml": miniMapping,
		".git/hooks.yaml":         miniMapping,
		"node_modules/pkg.yaml":   miniMapping,
		"vendor/lib.yaml":         miniMapping,
		"notes.txt":               miniMapping,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, content)
	}
	var got []string
	for _, f := range findMappings(dir) {
		got = append(got, f.path)
		if f.rules != 2 {
			t.Errorf("%s: rules = %d", f.path, f.rules)
		}
	}
	want := []string{"b.yml", filepath.Join("one", "two", "three.yaml"), "top.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("found %v, want %v", got, want)
	}
}

func TestMappingRulesRejectsOtherYAML(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		src   string
		rules int
		ok    bool
	}{
		"mapping":      {miniMapping, 2, true},
		"empty rules":  {"version: \"1\"\nrules: []\n", 0, true},
		"no version":   {"rules:\n  - name: a\n", 0, false},
		"no rules":     {"version: \"1\"\n", 0, false},
		"rules a map":  {"version: \"1\"\nrules: {a: b}\n", 0, false},
		"a list":       {"- a\n- b\n", 0, false},
		"not yaml":     {"version: [\n", 0, false},
		"compose file": {"services:\n  web:\n    image: x\n", 0, false},
		"bomb": {"a: &a [x,x,x,x,x,x,x,x,x]\nb: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
			"d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\ne: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\nversion: \"1\"\nrules: [*e]\n", 1, true},
		"huge": {"version: \"1\"\nrules: []\n# " + strings.Repeat("x", maxMappingBytes), 0, false},
	}
	for name, c := range cases {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".yaml")
		writeFile(t, p, c.src)
		if n, ok := mappingRules(p); n != c.rules || ok != c.ok {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", name, n, ok, c.rules, c.ok)
		}
	}
	if _, ok := mappingRules(filepath.Join(dir, "missing.yaml")); ok {
		t.Error("a missing file is a mapping")
	}
	if _, ok := mappingRules(dir); ok {
		t.Error("a folder is a mapping")
	}
	if _, ok := mappingRules("/dev/zero"); ok {
		t.Error("a device is a mapping")
	}
}

// withTimeout fails the test, rather than hanging the suite, when f blocks.
func withTimeout(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s blocked on a named pipe", what)
	}
}

func TestPipesNamedYAMLAreNeverOpened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no named pipes by mkfifo")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, filepath.Join(dir, "link.yml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "real.yaml"), miniMapping)

	withTimeout(t, "mappingRules", func() {
		if _, ok := mappingRules(fifo); ok {
			t.Error("a pipe is a mapping")
		}
	})
	withTimeout(t, "findMappings", func() {
		got := findMappings(dir)
		if len(got) != 1 || got[0].path != "real.yaml" {
			t.Errorf("found %v, want only real.yaml", got)
		}
	})
	withTimeout(t, "openPath", func() {
		if _, err := openPath(context.Background(), fifo); err == nil {
			t.Error("a pipe opened")
		}
	})
}
