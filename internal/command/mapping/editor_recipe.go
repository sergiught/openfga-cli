package mapping

import tea "charm.land/bubbletea/v2"

// openPayloadKind asks what the user is mapping. It is the first fork in the
// flow and the only place the Auth0 catalog is advertised, so adding a rule
// always passes through here.
func (m *wizardModel) openPayloadKind() {
	m.kindPick.SetCursor(0)
	m.push(screenPayloadKind)
}

func (m *wizardModel) keyPayloadKind(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.kindPick.Move(-1)
	case "down", "j":
		m.kindPick.Move(1)
	case "esc":
		m.pop()
	case "enter", " ":
		if m.kindPick.Selected().Value == "auth0" {
			m.openEventPick()
			return nil
		}
		m.paste.SetValue("")
		m.push(screenEventPaste)
	}
	return nil
}
