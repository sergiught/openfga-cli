package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

const twoTypeModel = `model
  schema 1.1

type user

type organization
  relations
    define member: [user]
`

// modelWs opens a fresh mapping beside a model.fga holding src ("" for none),
// calling Init as the program does, so the model dialog opens.
func modelWs(t *testing.T, src string, opts Options) *wsHarness {
	t.Helper()
	dir := t.TempDir()
	if src != "" {
		writeFile(t, filepath.Join(dir, "model.fga"), src)
	}
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{User: "user:{{ input.data.object.user.user_id }}", Relation: "member", Object: "organization:{{ input.data.object.organization.id }}"}},
		Sample: &mapping.Sample{Label: "sample", Event: memberAddedEvent()},
	}}}
	h := newWsHarness(t, opened{path: filepath.Join(dir, "mapping.yaml"), doc: d}, opts)
	h.cmds = append(h.cmds, h.w.Init())
	// Init also starts the badge run; let it land so busy counts only the fetch.
	h.run()
	return h
}

func storeOpts(load func(context.Context) (*openfga.AuthorizationModel, error)) Options {
	return Options{Store: "profile prod, store 01ARZ", LoadStoreModel: load}
}

func TestTheDialogOpensOnOpen(t *testing.T) {
	h := modelWs(t, twoTypeModel, Options{})
	if h.w.overlay == nil || !strings.Contains(h.view(), "Check against a model") {
		t.Fatalf("no model dialog on open:\n%s", h.view())
	}
	h.press("esc")
	if h.w.overlay != nil || h.w.model.label != noModel || h.w.sess.Index != nil {
		t.Fatalf("esc must keep the current model, which is none: %q", h.w.model.label)
	}
}

func TestTheDialogWaitsForALoadError(t *testing.T) {
	h := loadedWs(t, "rules: [")
	h.cmds = append(h.cmds, h.w.Init())
	if h.w.overlay != nil {
		t.Fatal("the dialog opened over a file that did not load")
	}
}

func TestAModelFileIsIndexed(t *testing.T) {
	h := modelWs(t, twoTypeModel, Options{})
	h.press("enter")
	if h.w.sess.Index == nil || h.w.model.label != "model ./model.fga (2 types)" {
		t.Fatalf("label = %q", h.w.model.label)
	}
	if want := filepath.Join(filepath.Dir(h.w.path), "model.fga"); h.w.model.path != want {
		t.Fatalf("path = %q, want %q", h.w.model.path, want)
	}
	if !strings.Contains(h.view(), "model ./model.fga (2 types)") {
		t.Fatalf("the label is not on screen:\n%s", h.view())
	}
}

func TestABadModelFileFallsBackToNone(t *testing.T) {
	h := modelWs(t, "type user\n  relations\n   nonsense [[[", Options{})
	h.press("enter")
	if h.w.sess.Index != nil || h.w.model.label != noModel || h.w.model.path != "" {
		t.Fatalf("model = %+v", h.w.model)
	}
	if !strings.Contains(h.toastText(), flat("could not read the model")) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

// A model with no types loads, but checking against it would pass every
// mapping while claiming the model was checked, as the CLI refuses it too.
func TestAModelFileWithNoTypesFallsBackToNone(t *testing.T) {
	h := modelWs(t, "{}", Options{})
	h.press("enter")
	if h.w.sess.Index != nil || h.w.model.label != noModel || h.w.model.path != "" {
		t.Fatalf("model = %+v", h.w.model)
	}
	if !strings.Contains(h.toastText(), flat("defines no types")) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestTheStoreModelLoadsInTheBackground(t *testing.T) {
	h := modelWs(t, twoTypeModel, storeOpts(func(context.Context) (*openfga.AuthorizationModel, error) {
		return testModel(), nil
	}))
	h.press("down", "enter")
	if h.w.busy != 1 || h.w.sess.Index != nil {
		t.Fatalf("busy = %d, index = %v while waiting", h.w.busy, h.w.sess.Index)
	}
	if sp := plain(h.w.spin.View()); sp == "" || !strings.Contains(h.view(), sp) {
		t.Fatalf("no spinner while waiting:\n%s", h.view())
	}
	h.run()
	if h.w.busy != 0 || h.w.sess.Index == nil {
		t.Fatalf("busy = %d, index = %v after the fetch", h.w.busy, h.w.sess.Index)
	}
	if !strings.HasPrefix(h.w.model.label, "store model 01J0 (") || h.w.model.path != "" {
		t.Fatalf("model = %+v", h.w.model)
	}
}

func TestAStaleModelFetchIsDropped(t *testing.T) {
	h := modelWs(t, twoTypeModel, storeOpts(func(ctx context.Context) (*openfga.AuthorizationModel, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	h.press("down", "enter")
	stale := h.w.model.gen
	if h.w.model.cancel == nil {
		t.Fatal("no fetch in flight")
	}
	h.w.openModelDialog()
	h.press("down", "down", "down", "enter") // no model
	if h.w.model.gen == stale {
		t.Fatal("the choice did not number itself")
	}
	h.send(storeModelMsg{gen: stale, model: testModel()})
	if h.w.sess.Index != nil || h.w.model.label != noModel || h.w.busy != 0 {
		t.Fatalf("the old fetch landed: model = %+v, busy = %d", h.w.model, h.w.busy)
	}
}

func TestAFailedModelFetchFallsBackToNone(t *testing.T) {
	h := modelWs(t, twoTypeModel, storeOpts(func(context.Context) (*openfga.AuthorizationModel, error) {
		return nil, errors.New("boom")
	}))
	h.press("enter") // the file first, so there is a model to lose
	h.w.openModelDialog()
	h.press("down", "enter")
	h.run()
	if h.w.sess.Index != nil || h.w.model.label != noModel || h.w.busy != 0 {
		t.Fatalf("model = %+v, busy = %d", h.w.model, h.w.busy)
	}
	if !strings.Contains(h.toastText(), flat("could not read the store model: boom")) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestModelChangeRelintsTheRules(t *testing.T) {
	h := modelWs(t, "model\n  schema 1.1\n\ntype user\n", Options{})
	missing := func() bool {
		for _, f := range h.w.findings {
			if strings.Contains(f.Message, "the model has no type") {
				return true
			}
		}
		return false
	}
	if missing() {
		t.Fatal("a finding about the model before one is chosen")
	}
	h.press("enter")
	if !missing() {
		t.Fatalf("Check did not re-run against the new model: %v", h.w.findings)
	}
	if _, sub := h.w.check.heading(); !strings.Contains(sub, "model ./model.fga (1 type)") {
		t.Fatalf("Check does not name the model: %q", sub)
	}
}

func TestTheDialogOpensOnTheFirstSuccessfulLoad(t *testing.T) {
	h := loadedWs(t, "rules: [")
	h.cmds = append(h.cmds, h.w.Init())
	writeFile(t, h.w.path, twoRules)
	h.w.reload()
	if h.w.overlay == nil {
		t.Fatal("the dialog did not open once the file loaded")
	}
}

// blockedStore is a store whose model fetch waits until release is closed.
func blockedStore(release <-chan struct{}) Options {
	return storeOpts(func(ctx context.Context) (*openfga.AuthorizationModel, error) {
		select {
		case <-release:
			return testModel(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
}

// A9: a store fetch that lands while the help is open applies the model and
// leaves the help open. An earlier landing closed whatever was on top.
func TestAModelFetchLandingUnderHelpKeepsHelpOpen(t *testing.T) {
	release := make(chan struct{})
	h := modelWs(t, twoTypeModel, blockedStore(release))
	h.press("down", "enter") // the store
	if h.w.busy != 1 || h.w.model.cancel == nil || h.w.sess.Index != nil {
		t.Fatalf("busy = %d, index = %v; want the fetch in flight", h.w.busy, h.w.sess.Index)
	}
	h.press("?")
	if !h.w.help {
		t.Fatal("? did not open the help")
	}
	close(release)
	h.run()
	if !h.w.help {
		t.Fatal("the model landing closed the help")
	}
	if !strings.HasPrefix(h.w.model.label, "store model 01J0 (") || h.w.sess.Index == nil || h.w.busy != 0 {
		t.Fatalf("model = %+v, busy = %d; want the store model in use", h.w.model, h.w.busy)
	}
	if v := h.view(); !strings.Contains(v, "Keys") || !strings.Contains(v, "store model 01J0") {
		t.Fatalf("the help or the model label is not on screen:\n%s", v)
	}
}

// A9: the same under the quit prompt, which stays up to be answered.
func TestAModelFetchLandingUnderTheQuitPromptKeepsIt(t *testing.T) {
	release := make(chan struct{})
	h := modelWs(t, twoTypeModel, blockedStore(release))
	h.press("down", "enter") // the store
	if h.w.busy != 1 || !h.w.dirty {
		t.Fatalf("busy = %d, dirty = %v; want the fetch in flight on an unsaved mapping", h.w.busy, h.w.dirty)
	}
	h.press("ctrl+c")
	if _, ok := h.w.overlay.top().(*quitPrompt); !ok {
		t.Fatal("ctrl+c on unsaved edits did not ask")
	}
	close(release)
	h.run()
	if h.quit || h.w.overlay == nil {
		t.Fatalf("quit = %v, overlay = %v; want the prompt still waiting", h.quit, h.w.overlay != nil)
	}
	if _, ok := h.w.overlay.top().(*quitPrompt); !ok {
		t.Fatalf("overlay is %T, want the quit prompt", h.w.overlay.top())
	}
	if !strings.HasPrefix(h.w.model.label, "store model 01J0 (") || h.w.sess.Index == nil {
		t.Fatalf("model = %+v; want the store model in use", h.w.model)
	}
	if v := h.view(); !strings.Contains(v, "Unsaved edits") || !strings.Contains(v, "store model 01J0") {
		t.Fatalf("the prompt or the model label is not on screen:\n%s", v)
	}
	h.press("esc")
	if h.w.overlay != nil || h.quit {
		t.Fatalf("esc: overlay = %v, quit = %v; want the prompt cancelled", h.w.overlay != nil, h.quit)
	}
}

func TestAModelChosenWhileBrokenIsNotAskedAgain(t *testing.T) {
	h := loadedWs(t, "rules: [")
	writeFile(t, filepath.Join(filepath.Dir(h.w.path), "model.fga"), twoTypeModel)
	h.cmds = append(h.cmds, h.w.Init())
	if !h.w.modelDue {
		t.Fatal("a broken file did not postpone the dialog")
	}
	h.cmds = append(h.cmds, h.w.openModelDialog())
	h.press("enter")
	h.run()
	if h.w.overlay != nil || h.w.modelDue {
		t.Fatalf("overlay = %v, modelDue = %v after choosing a model", h.w.overlay != nil, h.w.modelDue)
	}
	writeFile(t, h.w.path, twoRules)
	h.send(editorDoneMsg{})
	h.run()
	if h.w.loadErr != nil || h.w.overlay != nil {
		t.Fatalf("loadErr = %v, overlay = %v: the dialog opened again after the fix", h.w.loadErr, h.w.overlay != nil)
	}
}
