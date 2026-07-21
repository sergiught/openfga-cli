package modeltest

import "testing"

func TestDecodeTestFileCapturesTestSourcePosition(t *testing.T) {
	tf, err := decodeTestFile("tests/documents.test.yaml", []byte(`tests:
  - name: owner-is-viewer
    check:
      - user: user:anne
        object: document:1
        assertions: {viewer: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(tf.Tests) != 1 || tf.Tests[0].Line != 2 || tf.Tests[0].Column <= 0 {
		t.Fatalf("source position = %+v, want line 2 and a positive column", tf.Tests)
	}
}

func TestRunIncludesWorkspaceRelativeSource(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(t.Context(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) == 0 || res.Tests[0].File == "" || res.Tests[0].Line == 0 {
		t.Fatalf("run results missing source metadata: %+v", res.Tests)
	}
}
