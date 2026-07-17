package playground

import (
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// Stores tab: a permission rejection (401/403) listing stores must surface a
// dedicated "no permission" notice, not an empty "no stores" list plus a red
// error toast (which together misread as "this server has no stores").
func TestStoresForbiddenShowsWarning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()

	forbidden := &openfga.AuthenticationError{ErrorResponse: &openfga.ErrorResponse{
		Code: "forbidden", Message: "no permission to list stores",
	}}
	m, _ = m.Update(storesLoadedMsg{err: forbidden})

	mm := m.(Model)
	if !mm.storesForbidden {
		t.Fatal("a 401/403 listing stores must set storesForbidden")
	}
	if len(mm.stores) != 0 {
		t.Fatalf("a forbidden stores load must clear the list, got %d stores", len(mm.stores))
	}
	if levels := mm.toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Info {
		t.Fatalf("a forbidden stores load must push an Info notice, not an Error toast; got %v", levels)
	}

	// A later successful load clears the forbidden state.
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})
	if m.(Model).storesForbidden {
		t.Fatal("a successful stores load must clear storesForbidden")
	}
}

// TUI-37: modelIDRE must accept a valid ULID and reject a malformed pin, matching
// the SDK's own authorization-model-id validation.
func TestModelIDRE(t *testing.T) {
	valid := []string{
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"01KXR44ANHYANEBVKWPVNHJZCQ",
	}
	invalid := []string{
		"",
		"not-a-ulid",
		"01ARZ3NDEKTSV4RRFFQ69G5FA",   // 25 chars, too short
		"01ARZ3NDEKTSV4RRFFQ69G5FAVX", // 27 chars, too long
		"81ARZ3NDEKTSV4RRFFQ69G5FAV",  // first char out of [0-7]
		"01ILOU3NDEKTSV4RRFFQ69G5FA",  // contains excluded I/L/O/U
	}
	for _, id := range valid {
		if !modelIDRE.MatchString(id) {
			t.Errorf("modelIDRE should accept valid ULID %q", id)
		}
	}
	for _, id := range invalid {
		if modelIDRE.MatchString(id) {
			t.Errorf("modelIDRE should reject %q", id)
		}
	}
}

// TUI-37: the one-time boot notice (e.g. a dropped invalid pinned model id) is
// delivered through the Update loop as an Info toast.
func TestBootNoticeSurfacesToast(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()

	m, _ = m.Update(bootNoticeMsg{text: "ignored invalid pinned model id"})
	if levels := m.(Model).toasts.Levels(); len(levels) == 0 || levels[len(levels)-1] != toast.Info {
		t.Fatalf("bootNoticeMsg must push an Info toast, got %v", levels)
	}
}
