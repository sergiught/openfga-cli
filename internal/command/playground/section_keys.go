package playground

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// handleSectionKey handles a keypress when the right-hand panel has focus,
// dispatching per section (stores, model, tuples, changes, query, assertions,
// profiles). Split out of update.go to keep that file focused on the message
// dispatch loop.
func (m Model) handleSectionKey(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.section {
	case secProfiles:
		switch key {
		case "enter":
			if it, ok := m.profilesList.Selected(); ok {
				return m, m.switchProfile(it.ID)
			}
		case "n":
			return m.enterForm(formAddProfile)
		case "e":
			if it, ok := m.profilesList.Selected(); ok {
				p, _ := m.cli.Config.Get(it.ID)
				auth := p.ResolvedAuth()
				m.profileEditName = it.ID
				m.profileAuthMethod = auth.Method
				if m.profileAuthMethod == "" {
					m.profileAuthMethod = config.AuthNone
				}
				nm, cmd := m.enterForm(formEditProfile)
				mm := nm.(Model)
				mm.form.SetValues(profileFormValues(false, p.APIURL, auth))
				return mm, cmd
			}
			return m, nil
		case "d":
			if it, ok := m.profilesList.Selected(); ok {
				id := it.ID
				m.confirm = &confirmAction{
					action:  "Remove profile",
					subject: id,
					detail:  "This deletes its saved credentials.",
					run: func(m *Model) tea.Cmd {
						if err := m.cli.Config.Remove(id); err != nil {
							return m.toastErr("profile", err)
						}
						m.saveConfig()
						m.populateProfiles()
						m.status = "removed profile " + id
						return m.toasts.Push(toast.Success, m.status)
					},
				}
			}
			return m, nil
		}
		cmd := m.profilesList.Update(msg)
		return m, cmd

	case secStores:
		switch key {
		case "enter":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				return m, m.selectStore(m.stores[it.Index])
			}
		case "n":
			return m.enterForm(formCreateStore)
		case "d":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				s := m.stores[it.Index]
				m.confirm = &confirmAction{
					action:  "Delete store",
					subject: s.Name,
					detail:  "This permanently deletes the store and all its models, tuples and assertions.",
					run: func(m *Model) tea.Cmd {
						m.loading = true
						m.status = "deleting store…"
						return deleteStoreCmd(m.ctx, m.client, s.ID)
					},
				}
			}
			return m, nil
		case "r":
			m.loading = true
			return m, loadStoresCmd(m.ctx, m.client)
		}
		cmd := m.storesList.Update(msg)
		return m, cmd

	case secModel:
		switch key {
		case "e":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.editorOpen = true
			m.editorErr = ""
			if m.modelDSL != "" {
				m.editor.SetValue(m.modelDSL)
			} else {
				m.editor.SetValue(modelTemplate)
			}
			return m, m.editor.Focus()
		case "m":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.modelPicking = true
			m.loading = true
			return m, loadModelsCmd(m.ctx, m.client, m.storeID)
		case "r":
			if m.storeID != "" {
				m.loading = true
				return m, loadModelCmd(m.ctx, m.client, m.storeID)
			}
		case "up", "k", "shift+up":
			return m.scrollGraph(-graphLineStep)
		case "down", "j", "shift+down":
			return m.scrollGraph(graphLineStep)
		case "shift+left", "left", "h":
			return m.panGraph(-graphColStep)
		case "shift+right", "right", "l":
			return m.panGraph(graphColStep)
		case "pgup", "b":
			return m.scrollGraph(-m.graphVP.Height())
		case "pgdown", "f", " ":
			return m.scrollGraph(m.graphVP.Height())
		case "home", "g":
			return m.scrollGraphTo(0)
		case "end", "G":
			return m.scrollGraphTo(float64(m.graphMaxOffset()))
		}
		return m, nil

	case secTuples:
		switch key {
		case "a":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			return m.enterForm(formWriteTuple)
		case "d":
			if it, ok := m.tuplesList.Selected(); ok && it.Index < len(m.tuples) {
				k := m.tuples[it.Index].Key
				m.confirm = &confirmAction{
					action:  "Delete tuple",
					subject: fga.FormatTuple(k),
					run: func(m *Model) tea.Cmd {
						m.status = "deleting " + fga.FormatTuple(k) + "…"
						return writeTupleCmd(m.ctx, m.client, m.storeID, m.modelID, k, true)
					},
				}
			}
			return m, nil
		case "r":
			if m.storeID != "" {
				return m, loadTuplesCmd(m.ctx, m.client, m.storeID)
			}
		}
		cmd := m.tuplesList.Update(msg)
		return m, cmd

	case secChanges:
		switch key {
		case "r":
			if m.storeID != "" {
				m.loading = true
				return m, loadChangesCmd(m.ctx, m.client, m.storeID)
			}
		}
		cmd := m.changesList.Update(msg)
		return m, cmd

	case secQuery:
		// While the resolution tree is open it captures navigation.
		if m.showRes {
			switch key {
			case "r":
				m.showRes = false
			case "p":
				m.resPathOnly = !m.resPathOnly
				m.refreshResVP()
				m.resVP.SetYOffset(0)
			case "left", "h":
				m.resVP.ScrollLeft(4)
			case "right", "l":
				m.resVP.ScrollRight(4)
			default:
				var cmd tea.Cmd
				m.resVP, cmd = m.resVP.Update(msg)
				return m, cmd
			}
			return m, nil
		}
		switch key {
		case "i", "enter":
			return m, m.enterQueryEdit()
		case "tab":
			// Switch to the next mode and land in its first field, ready to type.
			m.cycleQueryMode(1)
			return m, m.enterQueryEdit()
		case "shift+tab":
			m.cycleQueryMode(-1)
			return m, m.enterQueryEdit()
		case "m":
			// Browse modes without entering the form.
			m.cycleQueryMode(1)
		case "r":
			// Show the Check resolution tree for the last check.
			if m.hasResult && m.result.badge {
				m.loading = true
				return m, expandCmd(m.ctx, m.client, m.storeID, m.modelID,
					m.result.vals[0], m.result.vals[1], m.result.vals[2])
			}
			m.status = "run a check first (r shows its resolution)"
		case "1", "2", "3", "4", "5", "6":
			// A digit addressing an existing history slot reruns it; "6"
			// never matches since history is capped at 5.
			if n := int(key[0] - '1'); n < len(m.history) {
				return m.rerunHistory(n)
			}
		}
		return m, nil

	case secAssertions:
		switch key {
		case "a":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.assertEditIdx = -1
			return m.enterForm(formWriteAssertion)
		case "e":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				m.assertEditIdx = it.Index
				nm, cmd := m.enterForm(formWriteAssertion)
				mm := nm.(Model)
				a := m.assertions[it.Index]
				mm.form.SetValues([]string{a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object, strconv.FormatBool(a.Expectation), formatContextualTuples(a.ContextualTuples), formatContextJSON(a.Context)})
				return mm, cmd
			}
			return m, nil
		case "d":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				idx := it.Index
				a := m.assertions[idx]
				exp := "expect allow"
				if !a.Expectation {
					exp = "expect deny"
				}
				label := a.TupleKey.User + " " + a.TupleKey.Relation + " " + a.TupleKey.Object
				m.confirm = &confirmAction{
					action:  "Delete assertion",
					subject: label,
					detail:  exp,
					run: func(m *Model) tea.Cmd {
						list := append([]openfga.Assertion{}, m.assertions...)
						list = append(list[:idx], list[idx+1:]...)
						m.status = "deleting assertion…"
						return writeAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, list)
					},
				}
			}
			return m, nil
		case "enter":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				a := m.assertions[it.Index]
				u, rel, obj := a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object
				// Run the assertion (updates its badge) and open its resolution
				// tree in the Query panel.
				m.section = secQuery
				m.result = queryResultMsg{badge: true, vals: [3]string{u, rel, obj}, mode: "check"}
				m.hasResult = true
				m.loading = true
				m.status = "resolving assertion…"
				return m, tea.Batch(
					runOneAssertionCmd(m.ctx, m.client, m.storeID, m.assertModelID, it.Index, a),
					expandCmd(m.ctx, m.client, m.storeID, m.assertModelID, u, rel, obj),
				)
			}
			return m, nil
		case "t":
			if len(m.assertions) == 0 {
				m.status = "no assertions to run"
				return m, nil
			}
			m.loading = true
			m.status = "running assertions…"
			return m, runAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, m.assertions)
		case "r":
			if m.storeID != "" {
				m.loading = true
				return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID)
			}
		}
		cmd := m.assertionsList.Update(msg)
		return m, cmd

	}
	return m, nil
}
