package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNarratorTreeAgreesWithEngineCheck cross-validates the narrator's
// Expand-derived resolution tree against the engine's own Check for every
// check assertion in every testdata workspace. It guards the
// ParseResolution/ExpandTree/MarkGranted pipeline: the marked tree's root
// granted state must always agree with the engine's authoritative verdict,
// across union, intersection, exclusion, tuple-to-userset, and conditioned
// rewrite forms.
func TestNarratorTreeAgreesWithEngineCheck(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil {
		t.Fatal(err)
	}

	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	workspaces := 0
	checked := 0

	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "ofga.yaml")); err != nil {
			continue
		}
		workspaces++

		ws, err := LoadWorkspace(dir)
		if err != nil {
			t.Fatalf("%s: load workspace: %v", dir, err)
		}

		for _, tf := range ws.TestFiles {
			modelPath, err := resolveModelPath(ws, tf)
			if err != nil {
				t.Fatalf("%s: %v", dir, err)
			}

			lm, err := loadModel(modelPath)
			if err != nil {
				t.Fatalf("%s: %v", dir, err)
			}

			for _, tt := range tf.Tests {
				// Fresh Setup per test, hermetic like run.go: every check
				// case in this test shares the one store built from the
				// test's own tuple world.
				tuples, err := resolveFixtures(ws, tf, tt, false, nil)
				if err != nil {
					t.Fatalf("%s %s/%s: resolve fixtures: %v", dir, FileStem(tf.Path), tt.Name, err)
				}

				protoTuples, err := toProtoTuples(tuples)
				if err != nil {
					t.Fatalf("%s %s/%s: %v", dir, FileStem(tf.Path), tt.Name, err)
				}

				storeID, modelID, err := eng.Setup(context.Background(), lm.Proto, protoTuples)
				if err != nil {
					t.Fatalf("%s %s/%s: setup: %v", dir, FileStem(tf.Path), tt.Name, err)
				}
				sc := Scope{StoreID: storeID, ModelID: modelID}

				for _, cc := range tt.Check {
					ctxTuples, err := toProtoTuples(cc.ContextualTuples)
					if err != nil {
						t.Fatalf("%s %s/%s: %v", dir, FileStem(tf.Path), tt.Name, err)
					}

					for _, relation := range sortedKeys(cc.Assertions) {
						checked++

						req := CheckReq{
							User:             cc.User,
							Relation:         relation,
							Object:           cc.Object,
							Context:          cc.Context,
							ContextualTuples: ctxTuples,
						}

						engineVerdict, err := eng.Check(context.Background(), sc, req)
						if err != nil {
							t.Errorf("%s %s/%s check %s %s %s: engine check: %v", dir, FileStem(tf.Path), tt.Name, cc.User, relation, cc.Object, err)
							continue
						}

						exp, err := trace(t.Context(), lm, eng, sc, req)
						if err != nil {
							t.Errorf("%s %s/%s check %s %s %s: trace: %v", dir, FileStem(tf.Path), tt.Name, cc.User, relation, cc.Object, err)
							continue
						}

						if exp.Verdict != engineVerdict {
							t.Errorf("%s %s/%s check %s %s %s: trace verdict=%v, engine check=%v", dir, FileStem(tf.Path), tt.Name, cc.User, relation, cc.Object, exp.Verdict, engineVerdict)
						}

						if exp.Tree == nil {
							t.Errorf("%s %s/%s check %s %s %s: tree root=<nil>, engine check=%v", dir, FileStem(tf.Path), tt.Name, cc.User, relation, cc.Object, engineVerdict)
							continue
						}

						if exp.Tree.Result != engineVerdict {
							t.Errorf("%s %s/%s check %s %s %s: tree root=%v, engine check=%v", dir, FileStem(tf.Path), tt.Name, cc.User, relation, cc.Object, exp.Tree.Result, engineVerdict)
						}
					}
				}
			}
		}
	}

	if workspaces == 0 {
		t.Fatal("no testdata workspaces found (expected dirs with ofga.yaml under testdata/)")
	}
	if checked == 0 {
		t.Fatal("no check assertions found across testdata workspaces")
	}

	t.Logf("cross-checked %d check assertions across %d workspaces", checked, workspaces)
}
