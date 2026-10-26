package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// wsHarness drives a workspace the way the program does, except that commands
// run only when the test says so.
type wsHarness struct {
	t    *testing.T
	w    *workspace
	cmds []tea.Cmd // returned, not run
	quit bool      // a command run returned tea.Quit
}

// newWsHarness opens o at 120×40. the model dialog is skipped (it opens from
// Init, which this does not call), so the workspace starts with no model.
func newWsHarness(t *testing.T, o opened, opts Options) *wsHarness {
	t.Helper()
	h := &wsHarness{t: t, w: newWorkspace(context.Background(), opts, o)}
	h.cmds = append(h.cmds, h.w.initStacks())
	h.resize(120, 40)
	return h
}

// loadedWs opens src written to a mapping file of its own.
func loadedWs(t *testing.T, src string) *wsHarness {
	t.Helper()
	return loadedWsWith(t, src, Options{})
}

// loadedWsWith is loadedWs with opts.
func loadedWsWith(t *testing.T, src string, opts Options) *wsHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := openPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return newWsHarness(t, o, opts)
}

// applyStoreID is the store applyTarget writes to.
const applyStoreID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

// applyTarget is an Apply target on store: store acme, profile default.
func applyTarget(store *fakeStore) *ApplyTarget {
	return &ApplyTarget{
		StoreID: applyStoreID,
		Profile: "default",
		Name:    func(context.Context) string { return "acme" },
		Reader:  store,
		Writer:  store,
		Hints:   func(error) []string { return nil },
		Resume: func(path, eventsPath string, from int) string {
			return fmt.Sprintf("ofga mappings apply %s --events %s --from-event %d", path, eventsPath, from)
		},
	}
}

// applyWs opens src, written to a mapping file of its own, with an Apply
// target on store.
func applyWs(t *testing.T, src string, store *fakeStore) *wsHarness {
	t.Helper()
	return loadedWsWith(t, src, Options{Apply: applyTarget(store)})
}

// freshWs opens d as a file the first save creates.
func freshWs(t *testing.T, d *mapping.Document) *wsHarness {
	t.Helper()
	return newWsHarness(t, opened{path: filepath.Join(t.TempDir(), "mapping.yaml"), doc: d}, Options{})
}

// wsKey is key() plus the keys only the workspace binds, built the way the
// terminal sends them.
func wsKey(s string) tea.KeyPressMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	return key(s)
}

func (h *wsHarness) press(keys ...string) {
	for _, k := range keys {
		h.send(wsKey(k))
	}
}

func (h *wsHarness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *wsHarness) send(msg tea.Msg) {
	if cmd := h.w.Update(msg); cmd != nil {
		h.cmds = append(h.cmds, cmd)
	}
}

func (h *wsHarness) resize(w, ht int) { h.send(tea.WindowSizeMsg{Width: w, Height: ht}) }

func (h *wsHarness) view() string { return plain(h.w.View()) }

// run runs the queued commands once, feeding their messages back. What those
// return is queued, not run. A command still running after the round's
// deadline is a timer (a debounce, a toast's expiry, a cursor blink) and is
// dropped: settleDebounce stands in for the one that matters.
func (h *wsHarness) run() {
	cmds := h.cmds
	h.cmds = nil
	// Long enough for a slow machine under -race, short of checkDelay. That
	// makes a debounce tick unlikely inside a round, not impossible: a slow
	// round can still let one through, which is why the loop below drops it.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	for _, msg := range collect(ctx, cmds) {
		if _, ok := msg.(tea.QuitMsg); ok {
			h.quit = true
			continue
		}
		// run drops debounceMsg: a slow round (the race detector) can outlast
		// checkDelay and let a tick through, and a test that did not ask for
		// it must not see one. Tests settle the debounce themselves.
		if _, ok := msg.(debounceMsg); ok {
			continue
		}
		h.send(msg)
	}
}

// collect runs cmds side by side and returns their messages in order, batches
// opened up, with what has not returned by ctx's deadline left out.
//
// A batch is told from a timer structurally: its command returns at once, with
// a tea.BatchMsg, and collect marks the command as a batch before recursing. At
// the deadline a marked command is waited for, which is quick, because the
// commands inside it stop at the same deadline; one still running is a timer
// and is dropped. No wait is timed, so the deadline itself never lets a timer
// in. A slow round can still let a debounce tick return before the deadline;
// run drops those, since it discards every debounceMsg.
func collect(ctx context.Context, cmds []tea.Cmd) []tea.Msg {
	type result struct {
		msgs  chan []tea.Msg
		batch chan struct{} // closed once the command is known to be a batch
	}
	results := make([]*result, len(cmds))
	for i, cmd := range cmds {
		if cmd == nil {
			continue
		}
		r := &result{msgs: make(chan []tea.Msg, 1), batch: make(chan struct{})}
		results[i] = r
		go func() {
			switch msg := cmd().(type) {
			case nil:
				r.msgs <- nil
			case tea.BatchMsg:
				close(r.batch)
				r.msgs <- collect(ctx, msg)
			default:
				r.msgs <- []tea.Msg{msg}
			}
		}()
	}
	var out []tea.Msg
	for _, r := range results {
		if r == nil {
			continue
		}
		select {
		case msgs := <-r.msgs:
			out = append(out, msgs...)
			continue
		case <-ctx.Done():
		}
		select {
		case <-r.batch:
			out = append(out, <-r.msgs...)
		case msgs := <-r.msgs:
			out = append(out, msgs...)
		default:
		}
	}
	return out
}

// settleDebounce sends the current debounce tick, as if typing had paused.
func (h *wsHarness) settleDebounce() { h.send(debounceMsg{gen: h.w.gen}) }

// topOf is the component on top of pane p's stack.
func (h *wsHarness) topOf(p pane) component { return h.w.stacks[p].top() }

func TestCollectKeepsNestedBatchesAndDropsTimers(t *testing.T) {
	timer := func(d time.Duration, msg tea.Msg) tea.Cmd {
		return func() tea.Msg { time.Sleep(d); return msg }
	}
	for range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		got := collect(ctx, []tea.Cmd{
			timer(3*time.Second, "toast"),
			timer(250*time.Millisecond, debounceMsg{gen: 1}),
			tea.Batch(timer(3*time.Second, "expiry"), tea.Batch(timer(3*time.Second, "x"), tea.Quit)),
		})
		cancel()
		if len(got) != 1 {
			t.Fatalf("collected %v, want only the quit", got)
		}
		if _, ok := got[0].(tea.QuitMsg); !ok {
			t.Fatalf("collected %T, want tea.QuitMsg", got[0])
		}
	}
}
