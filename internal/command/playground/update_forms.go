package playground

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// This file holds the full-panel takeover forms and the query form: entering
// them, rebuilding them per auth method, and advancing/submitting them. Split
// out of update.go to keep the message dispatch loop readable.
func (m Model) enterForm(kind formKind) (tea.Model, tea.Cmd) {
	switch {
	case kind == formCreateStore && m.storeCreating:
		m.status = "store creation already in progress"
		return m, nil
	case kind == formWriteTuple && m.tupleMutating:
		m.status = "tuple mutation already in progress"
		return m, nil
	case kind == formWriteAssertion && m.assertionsWriting:
		m.status = "assertion write already in progress"
		return m, nil
	}
	dw, dh := m.sh.DialogSize()
	m.formKind = kind
	switch kind {
	case formCreateStore:
		m.form = buildCreateStoreForm(dw)
	case formWriteTuple:
		m.form = buildWriteTupleForm(dw)
	case formWriteAssertion:
		m.form = buildWriteAssertionForm(dw)
	case formAddProfile:
		m.profileAuthMethod = config.AuthNone
		m.form = buildProfileForm(true, m.profileAuthMethod, dw)
	case formEditProfile:
		// m.profileAuthMethod is set by the caller from the profile being edited.
		m.form = buildProfileForm(false, m.profileAuthMethod, dw)
	}
	m.form.SetHeight(dh)
	// Emphasize the active field with the theme highlight; others stay plain.
	m.form.SetHighlight(style.FieldHighlight())
	return m, m.form.Init()
}

// profileMethodIndex is the form-field index of the auth-method selector:
// after name+api_url+store_id+model_id when adding, after
// api_url+store_id+model_id when editing.
func (m Model) profileMethodIndex() int {
	if m.formKind == formAddProfile {
		return 4
	}
	return 3
}

// profileFormMethod reads the auth method currently selected in the profile form.
func (m Model) profileFormMethod() string {
	vals := m.form.Values()
	if i := m.profileMethodIndex(); i < len(vals) {
		return vals[i]
	}
	return ""
}

// rebuildProfileForm rebuilds the profile form for the newly-selected auth
// method, preserving name/api_url/method and keeping focus on the selector.
func (m *Model) rebuildProfileForm() tea.Cmd {
	add := m.formKind == formAddProfile
	vals := m.form.Values()
	method := m.profileFormMethod()
	dw, dh := m.sh.DialogSize()
	m.form = buildProfileForm(add, method, dw)
	m.form.SetHeight(dh)
	m.form.SetHighlight(style.FieldHighlight())
	var pre []string
	idx := 0
	if add {
		pre = append(pre, vals[0])
		idx = 1
	}
	pre = append(pre, vals[idx], vals[idx+1], vals[idx+2], method)
	m.form.SetValues(pre)
	m.profileAuthMethod = method
	return m.form.FocusIndex(m.profileMethodIndex())
}

func (m Model) handleTakeoverForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.formKind = formNone
		return m, nil
	}
	return m.advanceTakeoverForm(msg)
}

// advanceTakeoverForm feeds any message to the takeover form and dispatches
// the resulting action once the form completes.
func (m Model) advanceTakeoverForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.form.Update(msg)
	// The profile form shows fields for the selected auth method; when the
	// method selector changes, rebuild the form for the new method.
	if (m.formKind == formAddProfile || m.formKind == formEditProfile) && !m.form.Completed() {
		if method := m.profileFormMethod(); method != m.profileAuthMethod {
			return m, m.rebuildProfileForm()
		}
	}
	if m.form.Completed() {
		vals := m.form.Values()
		kind := m.formKind
		m.formKind = formNone
		resume := func(message string) (tea.Model, tea.Cmd) {
			m.formKind = kind
			m.form.Resume()
			m.formErr = message
			return m, nil
		}
		switch kind {
		case formCreateStore:
			name := strings.TrimSpace(vals[0])
			if name == "" {
				return resume("store name required")
			}
			m.beginLoad()
			m.storeCreating = true
			m.storeCreateGen++
			m.mutationStatus = "creating store " + name + "…"
			m.status = m.mutationStatus
			return m, createStoreCmd(m.ctx, m.client, m.mutationOrigin("", "", m.storeCreateGen), name)
		case formWriteTuple:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				return resume(err.Error())
			}
			cond, err := parseCondition(vals[3], vals[4])
			if err != nil {
				return resume(err.Error())
			}
			key.Condition = cond
			m.beginLoad()
			m.tupleMutating = true
			m.tupleMutationGen++
			m.pendingTupleSelect = fga.FormatTuple(key)
			m.mutationStatus = "writing tuple " + fga.FormatTuple(key) + "…"
			m.status = m.mutationStatus
			return m, writeTupleCmd(m.ctx, m.client,
				m.mutationOrigin(m.storeID, m.modelID, m.tupleMutationGen), key, false)
		case formWriteAssertion:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				return resume(err.Error())
			}
			ctxTuples, err := parseContextualTuples(vals[4])
			if err != nil {
				return resume(err.Error())
			}
			ctxMap, err := parseContextJSON(vals[5])
			if err != nil {
				return resume(err.Error())
			}
			a := openfga.Assertion{
				TupleKey:         openfga.CheckRequestTupleKey{User: key.User, Relation: key.Relation, Object: key.Object},
				Expectation:      vals[3] == "true",
				ContextualTuples: ctxTuples,
				Context:          ctxMap,
			}
			list := append([]openfga.Assertion{}, m.assertions...)
			if m.assertEditIdx >= 0 && m.assertEditIdx < len(list) {
				list[m.assertEditIdx] = a
			} else {
				list = append(list, a)
			}
			m.beginLoad()
			m.assertionsWriting = true
			m.assertionWriteGen++
			m.pendingAssertionSelect = a.TupleKey.User + " " + a.TupleKey.Relation + " " + a.TupleKey.Object
			m.mutationStatus = "writing assertions…"
			m.status = m.mutationStatus
			return m, writeAssertionsCmd(m.ctx, m.client,
				m.mutationOrigin(m.storeID, m.assertModelID, m.assertionWriteGen), list)
		case formAddProfile:
			name, p := profileFromForm(true, vals)
			if name == "" {
				return resume("profile name required")
			}
			if _, exists := m.cli.Config.Get(name); exists {
				return resume("profile " + name + " already exists")
			}
			if err := p.Auth.Validate(); err != nil {
				return resume(err.Error())
			}
			previousActive := m.cli.Config.Active
			m.cli.Config.Set(name, p)
			if err := m.saveConfig(); err != nil {
				// The profile only exists in memory now. Clear the active guard
				// while removing it, then restore the pre-form active name.
				m.cli.Config.Active = ""
				_ = m.cli.Config.Remove(name)
				m.cli.Config.Active = previousActive
				m.formKind = kind
				m.form.Resume()
				m.formErr = err.Error()
				return m, m.configSaveErrCmd(err)
			}
			m.populateProfiles()
			m.status = "created profile " + name
			return m, m.toasts.Push(toast.Success, m.status)
		case formEditProfile:
			name := m.profileEditName
			existing, ok := m.cli.Config.Get(name)
			if !ok {
				m.status = "profile " + name + " no longer exists"
				return m, nil
			}
			prev := existing
			_, p := profileFromForm(false, vals)
			// A blank secret field means "keep the current secret" (we never
			// pre-fill secrets into the form). Carry the stored secret forward,
			// but only when the auth method is unchanged, so switching methods
			// can't smuggle a stale secret across.
			prevAuth := existing.ResolvedAuth()
			if p.Auth.Method == prevAuth.Method {
				preserveProfileSecrets(&p.Auth, prevAuth)
			}
			if err := p.Auth.Validate(); err != nil {
				return resume(err.Error())
			}
			var cleanupFields []string
			if p.Auth.Method != prevAuth.Method {
				cleanupFields = prevAuth.ConfiguredSecretFields()
			} else if prevAuth.PrivateKey != "" && p.Auth.PrivateKey == "" {
				cleanupFields = []string{"private_key"}
			}
			// Replace connection (url + store/model) and auth from the form.
			existing.APIURL, existing.StoreID, existing.ModelID, existing.Auth =
				p.APIURL, p.StoreID, p.ModelID, p.Auth
			m.cli.Config.Set(name, existing)
			var activeResolved config.Resolved
			var activeClient *openfga.Client
			if name == m.profile {
				var err error
				activeResolved, activeClient, err = m.resolvedClient()
				if err != nil {
					m.cli.Config.Set(name, prev)
					return resume(err.Error())
				}
			}
			saved, err := m.saveConfigWithSecretCleanup(name, false, cleanupFields...)
			if err != nil {
				if saved {
					m.populateProfiles()
					var reconnect tea.Cmd
					if name == m.profile {
						reconnect = m.activateResolved(activeResolved, activeClient, "updated profile "+name)
					}
					m.status = "profile updated, but saved credentials could not be deleted: " + err.Error()
					return m, tea.Batch(reconnect, m.toasts.Push(toast.Error, m.status))
				}
				// Roll back to the profile as it was before this edit, so the
				// in-memory config matches what's actually on disk, and don't
				// claim success or reconnect with the unsaved edit.
				m.cli.Config.Set(name, prev)
				m.formKind = kind
				m.form.Resume()
				m.formErr = err.Error()
				return m, m.configSaveErrCmd(err)
			}
			m.populateProfiles()
			// Editing the active profile changes the live connection — reconnect.
			if name == m.profile {
				return m, m.activateResolved(activeResolved, activeClient, "updated profile "+name)
			}
			m.status = "updated profile " + name
			return m, m.toasts.Push(toast.Success, m.status)
		}
	}
	return m, cmd
}

func preserveProfileSecrets(next *config.Auth, previous config.Auth) {
	if next.Token == "" {
		next.Token = previous.Token
	}
	if next.ClientSecret == "" {
		next.ClientSecret = previous.ClientSecret
	}
	if next.PrivateKey == "" && (next.KeyFile == "" || next.KeyFile == previous.KeyFile) {
		next.PrivateKey = previous.PrivateKey
	}
}

func (m Model) handleQueryForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// First esc drops to the (non-editing) panel layer, where r / history
		// digits / resolution live; a second esc returns to the tab selection.
		m.editing = false
		return m, nil
	case "ctrl+r":
		// Jump straight to the resolution tree for the last check, without first
		// dropping to the panel (esc then r). Plain "r" can't do this here — it is
		// a literal character for the query fields.
		if m.hasResult && m.result.badge {
			m.editing = false
			m.beginLoad()
			m.resGen++
			return m, expandCmd(m.ctx, m.client, m.storeID, m.modelID,
				m.result.vals[0], m.result.vals[1], m.result.vals[2], m.resGen)
		}
		m.status = "run a check first (ctrl+r shows its resolution)"
		return m, m.toasts.Push(toast.Info, m.status)
	case "tab":
		// tab keeps shifting modes even mid-edit, landing in the new mode's
		// first field. Field navigation stays on the arrows and enter.
		m.cycleQueryMode(1)
		return m, m.qform.Init()
	case "shift+tab":
		m.cycleQueryMode(-1)
		return m, m.qform.Init()
	}
	return m.advanceQueryForm(msg)
}

// advanceQueryForm feeds any message to the query form and runs the selected
// query once the form completes.
func (m Model) advanceQueryForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.qform.Update(msg)
	mode := queryModes[m.qmode]
	// The context toggle sits right after the mode's input fields (index n);
	// flipping it reveals/hides the extra fields, so rebuild the form when it
	// changes, preserving the main fields and staying on the toggle.
	n := queryFieldCount(mode)
	if !m.qform.Completed() {
		if show := m.qform.Values()[n] == "true"; show != m.qShowContext {
			m.qShowContext = show
			vals := m.qform.Values()
			m.rebuildQueryForm()
			m.qform.SetValues(vals[:n])
			return m, m.qform.FocusIndex(n)
		}
	}
	if m.qform.Completed() {
		vals := m.qform.Values()
		a := strings.TrimSpace(vals[0])
		b := strings.TrimSpace(vals[1])
		var c string
		if n == 3 {
			c = strings.TrimSpace(vals[2])
		}
		var qctx queryCtx
		var cerr error
		if m.qShowContext && len(vals) >= n+3 {
			qctx, cerr = parseQueryCtx(vals[n+1], vals[n+2])
		}
		// Stay in editing with a focused form, re-filled with the values just
		// run, so the core tweak-one-field-and-rerun loop doesn't force the user
		// to retype user/relation/object each time (esc drops to the panel for
		// r / history / resolution).
		m.rebuildQueryForm()
		m.qform.SetValues(vals)
		if cerr != nil {
			m.setQueryError(cerr.Error())
			return m, nil
		}
		if a == "" || b == "" || (n == 3 && c == "") {
			required := "user, relation and object are required"
			if n == 2 {
				required = "user and object are required"
			}
			m.setQueryError(required)
			return m, nil
		}
		// list-relations tests every relation on the object's type; resolve them
		// from the model up front so a missing or relationless type surfaces as a
		// query error instead of an empty run.
		var rels []string
		if mode == "list-relations" {
			var rerr error
			if rels, rerr = relationsForType(m.graph, b); rerr != nil {
				m.setQueryError(rerr.Error())
				return m, nil
			}
		}
		m.beginLoad()
		m.queryGen++
		gen := m.queryGen
		m.queryPendingGen = gen
		m.status = "running " + mode + "…"
		switch mode {
		case "check":
			return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
		case "list-objects":
			return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
		case "list-users":
			return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
		case "list-relations":
			return m, listRelationsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, rels, qctx, gen)
		}
	}
	return m, cmd
}

// rerunHistory replays history entry idx: switches to its mode, refills the
// query form with its values, and dispatches the same run command enter
// uses on form completion.
func (m Model) rerunHistory(idx int) (tea.Model, tea.Cmd) {
	h := m.history[idx]
	m.qmode = queryModeIndex(h.mode)
	m.rebuildQueryForm()
	m.qform.SetValues(h.vals[:queryFieldCount(h.mode)])
	m.editing = false
	a, b, c := h.vals[0], h.vals[1], h.vals[2]
	// Carry the recorded ABAC context + contextual tuples into the rerun so an
	// ABAC verdict resolves the same way it did originally; dropping them would
	// silently flip an ALLOWED result to DENIED.
	qctx := h.qctx
	m.beginLoad()
	m.queryGen++
	gen := m.queryGen
	m.queryPendingGen = gen
	m.status = "running " + queryModes[m.qmode] + "…"
	switch queryModes[m.qmode] {
	case "check":
		return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
	case "list-objects":
		return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
	case "list-users":
		return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx, gen)
	case "list-relations":
		rels, err := relationsForType(m.graph, b)
		if err != nil {
			// The load begun above never actually dispatched a request — free
			// its slot immediately instead of leaving the spinner stuck on it.
			m.endLoad()
			m.queryPendingGen = 0
			m.setQueryError(err.Error())
			return m, nil
		}
		return m, listRelationsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, rels, qctx, gen)
	}
	return m, nil
}
