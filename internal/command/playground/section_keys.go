package playground

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// handleSectionKey handles a keypress when the right-hand panel has focus,
// dispatching per section (stores, model, tuples, changes, query, assertions,
// profiles). Split out of update.go to keep that file focused on the message
// dispatch loop.
func (m Model) handleSectionKey(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// "v" toggles the compact, full-width list view for the sections that
	// support it. It's a no-op everywhere else.
	if key == "v" {
		switch m.section {
		case secTuples, secChanges, secAssertions:
			m.compact = !m.compact
			m.populateTuples()
			m.populateChanges()
			m.populateAssertions()
			m.resize()
			if m.compact {
				m.status = "compact view"
			} else {
				m.status = "detail view"
			}
			return m, nil
		}
	}
	switch m.section {
	case secProfiles:
		switch key {
		case "enter":
			if it, ok := m.profilesList.Selected(); ok {
				return m, m.switchProfile(it.ID)
			}
		case "a":
			return m.enterForm(formAddProfile)
		case "e":
			if it, ok := m.profilesList.Selected(); ok {
				if it.ID == m.ephemeralProfile {
					m.status = "the seeded profile is ephemeral and can't be edited"
					return m, nil
				}
				p, _ := m.cli.Config.Get(it.ID)
				auth := p.ResolvedAuth()
				m.profileEditName = it.ID
				m.profileAuthMethod = auth.Method
				if m.profileAuthMethod == "" {
					m.profileAuthMethod = config.AuthNone
				}
				nm, cmd := m.enterForm(formEditProfile)
				mm := nm.(Model)
				mm.form.SetValues(profileFormValues(false, p.APIURL, p.StoreID, p.ModelID, auth))
				return mm, cmd
			}
			return m, nil
		case "d":
			if it, ok := m.profilesList.Selected(); ok {
				id := it.ID
				if id == m.profile {
					m.status = "cannot remove the active profile " + id + "; switch first"
					return m, m.toasts.Push(toast.Error, m.status)
				}
				m.confirm = &confirmAction{
					action:  "Remove profile",
					subject: id,
					detail:  "This deletes its saved credentials.",
					run: func(m *Model) tea.Cmd {
						prev, _ := m.cli.Config.Get(id)
						if err := m.cli.Config.Remove(id); err != nil {
							return m.toastErr("profile", err)
						}
						saved, err := m.saveConfigWithSecretCleanup(id, true, prev.Auth.ConfiguredSecretFields()...)
						if err != nil {
							if saved {
								m.populateProfiles()
								m.status = "profile removed, but saved credentials could not be deleted: " + err.Error()
								return m.toasts.Push(toast.Error, m.status)
							}
							// Roll back the in-memory removal so the profile list stays
							// consistent with what's actually on disk, and don't claim
							// success.
							m.cli.Config.Set(id, prev)
							return m.configSaveErrCmd(err)
						}
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
		case "a", "n":
			return m.enterForm(formCreateStore)
		case "d":
			if it, ok := m.storesList.Selected(); ok && it.Index < len(m.stores) {
				s := m.stores[it.Index]
				m.confirm = &confirmAction{
					action:  "Delete store",
					subject: s.Name,
					detail:  "This permanently deletes the store and all its models, tuples and assertions.",
					require: s.ID,
					run: func(m *Model) tea.Cmd {
						if m.storeDeleting {
							m.status = "store deletion already in progress"
							return nil
						}
						m.beginLoad()
						m.storeDeleting = true
						m.storeDeleteGen++
						m.mutationStatus = "deleting store " + s.Name + "…"
						m.status = m.mutationStatus
						return deleteStoreCmd(m.ctx, m.client,
							m.mutationOrigin("", "", m.storeDeleteGen), s.ID)
					},
				}
			}
			return m, nil
		case "r":
			m.beginLoad()
			// A manual reload racing the refresh a create/delete already
			// triggers (or another manual reload) must not let the older of
			// the two overwrite the newer list.
			m.storesGen++
			return m, loadStoresCmd(m.ctx, m.client, m.storesGen)
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
			m.refreshEditorDiagnostics()
			m.editorTop = 0
			return m, m.editor.Focus()
		case "m":
			if m.storeID == "" {
				m.status = "select a store first"
				return m, nil
			}
			m.modelPicking = true
			m.beginLoad()
			// A rapid close/reopen of the picker must not let an older list
			// response (from a previous open) overwrite the model list a newer
			// open already applied.
			m.modelsGen++
			return m, loadModelsCmd(m.ctx, m.client, m.storeID, m.modelsGen)
		case "r":
			if m.storeID != "" {
				m.beginLoad()
				m.modelGen++
				return m, loadModelCmd(m.ctx, m.client, m.storeID, m.modelGen)
			}
		case "v":
			if len(m.graph.Types) == 0 {
				return m, nil
			}
			m.graphView = 1 - m.graphView
			m.graphVP.SetContent(m.renderGraph())
			m.resetGraphScroll()
			if m.graphView == 1 {
				m.status = "weighted graph — v for diagram"
			} else {
				m.status = "model diagram — v for weighted graph"
			}
			return m, nil
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
		case "pgdown", "f", "space":
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
						if m.tupleMutating {
							m.status = "tuple mutation already in progress"
							return nil
						}
						m.beginLoad()
						m.tupleMutating = true
						m.tupleMutationGen++
						m.mutationStatus = "deleting tuple " + fga.FormatTuple(k) + "…"
						m.status = m.mutationStatus
						return writeTupleCmd(m.ctx, m.client,
							m.mutationOrigin(m.storeID, m.modelID, m.tupleMutationGen), k, true)
					},
				}
			}
			return m, nil
		case "r":
			if m.storeID != "" {
				m.beginLoad()
				m.tuplesGen++
				return m, loadTuplesCmd(m.ctx, m.client, m.storeID, m.tuplesGen)
			}
		}
		cmd := m.tuplesList.Update(msg)
		return m, cmd

	case secChanges:
		switch key {
		case "r":
			if m.storeID != "" {
				m.beginLoad()
				m.changesGen++
				return m, loadChangesCmd(m.ctx, m.client, m.storeID, m.changesGen)
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
				m.setResPathOnly(!m.resPathOnly)
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
				m.beginLoad()
				m.resGen++
				return m, expandCmd(m.ctx, m.client, m.storeID, m.modelID,
					m.result.vals[0], m.result.vals[1], m.result.vals[2], m.resGen)
			}
			m.status = "run a check first (r shows its resolution)"
			return m, m.toasts.Push(toast.Info, m.status)
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
				a := m.assertions[it.Index]
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
						if m.assertionsWriting {
							m.status = "assertion write already in progress"
							return nil
						}
						idx := -1
						for i := range m.assertions {
							if reflect.DeepEqual(m.assertions[i], a) {
								idx = i
								break
							}
						}
						if idx < 0 {
							m.status = "assertion changed; nothing deleted"
							return m.toasts.Push(toast.Error, m.status)
						}
						list := append([]openfga.Assertion{}, m.assertions...)
						list = append(list[:idx], list[idx+1:]...)
						m.beginLoad()
						m.assertionsWriting = true
						m.assertionWriteGen++
						m.mutationStatus = "deleting assertion…"
						m.status = m.mutationStatus
						return writeAssertionsCmd(m.ctx, m.client,
							m.mutationOrigin(m.storeID, m.assertModelID, m.assertionWriteGen), list)
					},
				}
			}
			return m, nil
		case "enter":
			if it, ok := m.assertionsList.Selected(); ok && it.Index < len(m.assertions) {
				a := m.assertions[it.Index]
				u, rel, obj := a.TupleKey.User, a.TupleKey.Relation, a.TupleKey.Object
				// Run the assertion (updates its badge) and open its resolution
				// tree in the Query panel. Seed only the query coordinates for the
				// resolution header — leave hasResult false so no verdict shows
				// until the real Check lands (assertOneMsg); pre-populating a
				// zero-value result would flash a fabricated "✗ DENIED".
				m.section = secQuery
				m.result = queryResultMsg{badge: true, vals: [3]string{u, rel, obj}, mode: "check"}
				m.hasResult = false
				// Two concurrent commands fire here (the assertion check and its
				// resolution tree) — each needs its own begin so the spinner
				// doesn't stop the moment whichever lands first.
				m.beginLoad()
				m.beginLoad()
				m.assertGen++
				m.resGen++
				m.status = "resolving assertion…"
				return m, tea.Batch(
					runOneAssertionCmd(m.ctx, m.client, m.storeID, m.assertModelID, it.Index, a, m.assertGen),
					expandCmd(m.ctx, m.client, m.storeID, m.assertModelID, u, rel, obj, m.resGen),
				)
			}
			return m, nil
		case "t":
			if len(m.assertions) == 0 {
				m.status = "no assertions to run"
				return m, nil
			}
			m.beginLoad()
			m.assertGen++
			m.status = "running assertions…"
			return m, runAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, m.assertions, m.assertGen)
		case "r":
			if m.storeID != "" {
				m.beginLoad()
				m.assertLoadGen++
				return m, loadAssertionsCmd(m.ctx, m.client, m.storeID, m.modelID, m.assertLoadGen)
			}
		}
		cmd := m.assertionsList.Update(msg)
		return m, cmd

	case secAPILogs:
		if m.recorder == nil {
			return m, nil
		}
		entries := m.recorder.Snapshot()
		switch key {
		case "up":
			if m.apiLogSel > 0 {
				m.apiLogSel--
				m.apiLogHScroll = 0
				m.refreshAPILogVP()
				m.apiLogVP.GotoTop()
			}
			return m, nil
		case "down":
			if m.apiLogSel < len(entries)-1 {
				m.apiLogSel++
				m.apiLogHScroll = 0
				m.refreshAPILogVP()
				m.apiLogVP.GotoTop()
			}
			return m, nil
		case "j":
			// Scroll the detail section down (arrows drive the list, so the body
			// gets its own keys).
			m.apiLogVP.ScrollDown(apiLogScrollStep)
			return m, nil
		case "k":
			m.apiLogVP.ScrollUp(apiLogScrollStep)
			return m, nil
		case "tab":
			// Cycle the detail sub-section (Req/Resp headers/body); the section
			// keeps across entry changes so you can compare the same part.
			m.apiLogTab = (m.apiLogTab + 1) % len(apiLogTabs)
			m.refreshAPILogVP()
			m.apiLogVP.GotoTop()
			return m, nil
		case "shift+tab":
			m.apiLogTab = (m.apiLogTab + len(apiLogTabs) - 1) % len(apiLogTabs)
			m.refreshAPILogVP()
			m.apiLogVP.GotoTop()
			return m, nil
		case "left":
			// Scroll the selected row's URL left so the start comes back.
			m.apiLogHScroll -= apiLogHStep
			if m.apiLogHScroll < 0 {
				m.apiLogHScroll = 0
			}
			return m, nil
		case "right":
			// Scroll the selected row's URL right to read a long path in full.
			if pathLen := m.selectedAPILogPathLen(entries); m.apiLogHScroll+apiLogHStep <= pathLen {
				m.apiLogHScroll += apiLogHStep
			}
			return m, nil
		case "pgdown", "f", "space":
			m.apiLogVP.PageDown()
			return m, nil
		case "pgup", "b":
			m.apiLogVP.PageUp()
			return m, nil
		case "d":
			m.apiLogVP.HalfPageDown()
			return m, nil
		case "u":
			m.apiLogVP.HalfPageUp()
			return m, nil
		case "c":
			m.apiLogPretty = !m.apiLogPretty
			m.refreshAPILogVP()
			m.apiLogVP.GotoTop()
			if m.apiLogPretty {
				m.status = "readable bodies"
			} else {
				m.status = "compact bodies"
			}
			return m, nil
		case "x":
			m.recorder.Clear()
			m.apiLogSel = 0
			m.apiLogHScroll = 0
			m.refreshAPILogVP()
			m.apiLogVP.GotoTop()
			m.status = "cleared API logs"
			return m, nil
		}
		return m, nil

	case secTestResults:
		return m.handleTestResultsKey(key)
	}
	return m, nil
}

// handleTestResultsKey handles a keypress in the Tests section: file/test
// navigation and the run/edit/new/delete/coverage/explain actions. Split out of
// handleSectionKey (and merged from what were two sequential switches) so the
// whole Tests behavior lives in one place, with one switch. Dead-end feedback
// rides on toasts, since the Tests footer renders no status text (see wbStatus).
func (m Model) handleTestResultsKey(key string) (tea.Model, tea.Cmd) {
	if m.wb.running {
		switch key {
		case "n", "e", "r", "R", "c", "v", "up", "k", "down", "j", "enter", "space", "d":
			return m, m.wbStatus(toast.Info, "tests running…")
		}
	}
	switch key {
	case "n":
		return m.openWorkbenchNewPrompt()
	case "e":
		return m.openWorkbenchEditor()
	case "r":
		return m.runSuite("")
	case "R":
		tf, _, ok := m.wbSelectedFile()
		if !ok {
			return m, nil
		}
		return m.runSuite(wbFileStem(m.wb.workspace, tf) + "/*")
	case "c":
		if m.wb.coverage == nil {
			m.wb.showCoverage = false
			// Distinguish "ran, but coverage couldn't be built" (e.g. a
			// multi-model workspace) from "not run yet" so the hint is
			// actionable rather than a misleading "run first".
			if m.wb.coverageErr != "" {
				return m, m.wbStatus(toast.Info, "coverage unavailable: "+m.wb.coverageErr)
			}
			return m, m.wbStatus(toast.Info, "run tests first (r) to see coverage")
		}
		m.wb.showCoverage = !m.wb.showCoverage
		if m.wb.showCoverage {
			m.wb.verbose = false
			m.wb.covScroll = 0
			return m, m.wbStatus(toast.Info, "coverage — c to hide")
		}
		return m, m.wbStatus(toast.Info, "coverage hidden")
	case "v":
		return m, m.toggleVerbose()
	case "up", "k":
		// The coverage report isn't a cursor list — while it's shown, up/k
		// scroll it (mirroring the wheel; see handleWheel) instead of moving
		// the tree's selection, which would be invisible behind the coverage
		// pane and desync the tree from what the arrow keys just did.
		if m.wb.showCoverage && m.wb.coverage != nil {
			w, h := m.contentSize()
			m.wb.covScroll = clampScroll(m.wb.covScroll, true, renderWorkbenchCoverage(m.wb.coverage, w), h)
			return m, nil
		}
		m.wb.treeSel--
		m.wb.detailScroll = 0
		m.clampWbTreeSel()
	case "down", "j":
		if m.wb.showCoverage && m.wb.coverage != nil {
			w, h := m.contentSize()
			m.wb.covScroll = clampScroll(m.wb.covScroll, false, renderWorkbenchCoverage(m.wb.coverage, w), h)
			return m, nil
		}
		m.wb.treeSel++
		m.wb.detailScroll = 0
		m.clampWbTreeSel()
	case "enter", "space":
		// On a file node, toggle whether its tests are shown; on a test
		// node, drill in by toggling its explanation (same as "v").
		if node, ok := m.wbSelectedNode(); ok {
			switch node.kind {
			case wbNodeFile:
				m.wbToggleCollapse(m.wb.files[node.fileIdx].Path)
				m.clampWbTreeSel()
			case wbNodeTest:
				return m, m.toggleVerbose()
			}
		}
	case "d":
		tf, _, ok := m.wbSelectedFile()
		if m.wb.workspace == nil || !ok {
			return m, m.wbStatus(toast.Info, "no test file to delete")
		}
		m.confirm = &confirmAction{
			action:  "Delete test file",
			subject: filepath.Base(tf.Path),
			detail:  tf.Path,
			run: func(m *Model) tea.Cmd {
				// Path-safety: never remove a file outside the workspace root, even
				// if tf.Path were somehow tampered with (it always comes from the
				// loaded workspace, so this is defense-in-depth, mirroring the same
				// guard on the editor's write path).
				if m.wb.workspace == nil || !withinRoot(m.wb.workspace.Root, tf.Path) {
					return m.wbStatus(toast.Error, "refused: path outside workspace")
				}
				if err := os.Remove(tf.Path); err != nil {
					return m.wbStatus(toast.Error, "delete failed: "+err.Error())
				}
				// Reload the workspace so the parsed test files reflect the
				// deletion. A reload failure (e.g. a bare test-file workspace whose
				// only file was just deleted, leaving no manifest to re-resolve)
				// must not crash — fall back to an empty file list.
				if ws, err := modeltest.LoadWorkspace(m.wb.workspace.Root); err == nil {
					m.wb.workspace = ws
					m.wb.files = ws.TestFiles
				} else {
					m.wb.files = nil
				}
				m.clampWbTreeSel()
				// Confirm the deletion with a visible toast — the Tests footer shows
				// no status text, so a status-only line would leave no feedback.
				done := m.wbStatus(toast.Success, "deleted "+filepath.Base(tf.Path))
				if len(m.wb.files) == 0 {
					return done
				}
				// Re-run the suite so results/coverage no longer reflect the
				// deleted file.
				_, cmd := m.runSuite("")
				return tea.Batch(done, cmd)
			},
		}
		return m, nil
	}
	return m, nil
}
