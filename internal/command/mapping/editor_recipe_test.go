package mapping

import (
	"strings"
	"testing"
)

// Adding a rule offers the catalog, every time — not only on first run, and not
// buried behind ctrl+e once the user is already lost in a blank form.
func TestAddingARuleOffersThePayloadKind(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"))
	if m.top() != screenPayloadKind {
		t.Fatalf("top = %v, want the payload-kind screen", m.top())
	}
	out := m.viewString()
	for _, want := range []string{"Auth0", "JSON"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from the payload-kind screen:\n%s", want, out)
		}
	}
}

func TestChoosingAuth0OpensTheEventCatalog(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("enter"))
	if m.top() != screenEventPick {
		t.Fatalf("top = %v, want the event picker", m.top())
	}
}

func TestChoosingOwnPayloadOpensThePasteScreen(t *testing.T) {
	m := atRulesHub(t)
	send(m, key("a"), key("down"), key("enter"))
	if m.top() != screenEventPaste {
		t.Fatalf("top = %v, want the paste screen", m.top())
	}
}
