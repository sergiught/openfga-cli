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
	dw, _ := m.sh.DialogSize()
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
	// Emphasize the active field with the theme highlight; others stay plain.
	m.form.SetHighlight(style.FieldHighlight())
	return m, m.form.Init()
}

// profileMethodIndex is the form-field index of the auth-method selector:
// after name+api_url when adding, after api_url when editing.
func (m Model) profileMethodIndex() int {
	if m.formKind == formAddProfile {
		return 2
	}
	return 1
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
	dw, _ := m.sh.DialogSize()
	m.form = buildProfileForm(add, method, dw)
	m.form.SetHighlight(style.FieldHighlight())
	var pre []string
	idx := 0
	if add {
		pre = append(pre, vals[0])
		idx = 1
	}
	pre = append(pre, vals[idx], method)
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
		switch kind {
		case formCreateStore:
			name := strings.TrimSpace(vals[0])
			if name == "" {
				m.formErr = "store name required"
				return m, nil
			}
			m.status = "creating store " + name + "…"
			return m, createStoreCmd(m.ctx, m.client, name)
		case formWriteTuple:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				m.formErr = err.Error()
				return m, nil
			}
			cond, err := parseCondition(vals[3], vals[4])
			if err != nil {
				m.formErr = err.Error()
				return m, nil
			}
			key.Condition = cond
			m.status = "writing " + fga.FormatTuple(key) + "…"
			return m, writeTupleCmd(m.ctx, m.client, m.storeID, m.modelID, key, false)
		case formWriteAssertion:
			key, err := fga.ParseTuple(vals[0], vals[1], vals[2])
			if err != nil {
				// Surface any failure adding an assertion in the modal, not the footer.
				m.formErr = err.Error()
				return m, nil
			}
			ctxTuples, err := parseContextualTuples(vals[4])
			if err != nil {
				m.formErr = err.Error()
				return m, nil
			}
			ctxMap, err := parseContextJSON(vals[5])
			if err != nil {
				m.formErr = err.Error()
				return m, nil
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
			m.status = "writing assertions…"
			return m, writeAssertionsCmd(m.ctx, m.client, m.storeID, m.assertModelID, list)
		case formAddProfile:
			name, p := profileFromForm(true, vals)
			if name == "" {
				m.status = "profile name required"
				return m, nil
			}
			if _, exists := m.cli.Config.Get(name); exists {
				m.status = "profile " + name + " already exists"
				return m, nil
			}
			m.cli.Config.Set(name, p)
			m.saveConfig()
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
			_, p := profileFromForm(false, vals)
			// A blank secret field means "keep the current secret" (we never
			// pre-fill secrets into the form). Carry the stored secret forward,
			// but only when the auth method is unchanged, so switching methods
			// can't smuggle a stale secret across.
			prev := existing.ResolvedAuth()
			if p.Auth.Method == prev.Method {
				if p.Auth.Token == "" {
					p.Auth.Token = prev.Token
				}
				if p.Auth.ClientSecret == "" {
					p.Auth.ClientSecret = prev.ClientSecret
				}
			}
			// Keep the auto-managed store/model; replace connection + auth.
			existing.APIURL, existing.Auth = p.APIURL, p.Auth
			m.cli.Config.Set(name, existing)
			m.saveConfig()
			m.populateProfiles()
			// Editing the active profile changes the live connection — reconnect.
			if name == m.cli.Config.Active {
				return m, m.reloadActive("updated profile " + name)
			}
			m.status = "updated profile " + name
			return m, m.toasts.Push(toast.Success, m.status)
		}
	}
	return m, cmd
}

func (m Model) handleQueryForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// First esc drops to the (non-editing) panel layer, where r / history
		// digits / resolution live; a second esc returns to the tab selection.
		m.editing = false
		return m, nil
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
		m.loading = true
		m.status = "running " + mode + "…"
		switch mode {
		case "check":
			return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		case "list-objects":
			return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		case "list-users":
			return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
		case "list-relations":
			return m, listRelationsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, rels, qctx)
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
	m.loading = true
	m.status = "running " + queryModes[m.qmode] + "…"
	switch queryModes[m.qmode] {
	case "check":
		return m, checkCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
	case "list-objects":
		return m, listObjectsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
	case "list-users":
		return m, listUsersCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, c, qctx)
	case "list-relations":
		rels, err := relationsForType(m.graph, b)
		if err != nil {
			m.setQueryError(err.Error())
			return m, nil
		}
		return m, listRelationsCmd(m.ctx, m.client, m.storeID, m.modelID, a, b, rels, qctx)
	}
	return m, nil
}
