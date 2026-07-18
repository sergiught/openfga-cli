package configcmd

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
)

// Probe caps: the wizard fetches at most this many stores/models to fill its
// pickers. Setup only needs enough to pick one; large deployments still work
// (a "more exist" note is shown) without paging the whole account.
const (
	storeProbeCap = 100
	modelProbeCap = 50
)

// Sentinel picker values that select an action rather than a concrete ID.
// Empty string is used directly for "no store" / "always latest model".
const (
	valManual = "\x00manual"
	valNone   = ""
)

// step is one screen of the guided tour.
type step int

const (
	stepWelcome    step = iota
	stepConnection      // API URL
	stepAuthMethod      // pick none/api_token/client_credentials/private_key_jwt
	stepAuthDetails
	stepProbe // live connection test + store fetch
	stepStore // pick or type a store
	stepModel // pick or type a model
	stepReview
)

// wizardValues is what the tour collects and hands back to init.
type wizardValues struct {
	apiURL  string
	auth    config.Auth
	storeID string
	modelID string
}

// wizardSeed pre-fills the tour from CLI flags and the resolved profile name.
type wizardSeed struct {
	profile   string // display only; the CLI arg owns the profile name
	overwrite bool   // the profile already exists on disk; warn before saving
	apiURL    string
	storeID   string
	modelID   string
}

// prober performs the wizard's live probes against a candidate connection.
// The real implementation builds an openfga client; tests supply a fake.
type prober interface {
	stores(ctx context.Context) ([]openfga.Store, error)
	models(ctx context.Context, storeID string) ([]openfga.AuthorizationModel, error)
}

// --- async messages ---

type storesMsg struct {
	stores []openfga.Store
	capped bool
	err    error
}

type modelsMsg struct {
	storeID string // store the load ran against, to drop a stale response
	models  []openfga.AuthorizationModel
	err     error
}

// wizardModel drives the guided `ofga init` tour.
type wizardModel struct {
	ctx     context.Context
	timeout time.Duration
	seed    wizardSeed

	// newProber builds a prober for a candidate connection; swapped in tests.
	newProber func(apiURL string, auth config.Auth, timeout time.Duration) prober

	step          step
	width, height int
	values        wizardValues

	connForm *field.Form // API URL
	method   string      // chosen auth method
	authPick *picker     // auth method chooser
	authForm *field.Form // secret fields for the chosen method (nil for none)

	spin spinner.Model

	// probe state
	probed  bool
	stores  []openfga.Store
	capped  bool
	connErr error

	// store step
	storePick   *picker
	storeManual bool
	storeForm   *field.Form

	// model step
	modelLoading bool
	models       []openfga.AuthorizationModel
	modelErr     error
	modelPick    *picker
	modelManual  bool
	modelForm    *field.Form

	// outcome
	done      bool
	cancelled bool
}

func newWizard(ctx context.Context, timeout time.Duration, seed wizardSeed) *wizardModel {
	apiURL := seed.apiURL
	if apiURL == "" {
		apiURL = config.DefaultAPIURL
	}
	connForm := field.NewForm(
		field.New("API URL", config.DefaultAPIURL).WithValidate(validateURL),
	)
	connForm.SetValues([]string{apiURL})

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(style.Primary)),
	)

	m := &wizardModel{
		ctx:       ctx,
		timeout:   timeout,
		seed:      seed,
		newProber: newClientProber,
		connForm:  connForm,
		authPick:  newAuthPicker(),
		spin:      sp,
	}
	m.values.apiURL = apiURL
	return m
}

func (m *wizardModel) Init() tea.Cmd { return nil }

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyWidth()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case storesMsg:
		m.probed = true
		m.stores, m.capped, m.connErr = msg.stores, msg.capped, msg.err
		return m, nil

	case modelsMsg:
		if msg.storeID != m.values.storeID {
			return m, nil // stale response for a store we've moved off
		}
		m.modelLoading = false
		m.models, m.modelErr = msg.models, msg.err
		m.buildModelPicker()
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *wizardModel) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	case "esc":
		return m, m.back()
	}

	switch m.step {
	case stepWelcome:
		if k.String() == "enter" {
			return m, m.enter(stepConnection)
		}
	case stepConnection:
		cmd := m.connForm.Update(k)
		if m.connForm.Completed() {
			m.values.apiURL = strings.TrimSpace(m.connForm.Values()[0])
			return m, m.enter(stepAuthMethod)
		}
		return m, cmd
	case stepAuthMethod:
		cmd, done := m.pickerKey(m.authPick, k)
		if !done {
			return m, cmd
		}
		m.method = m.authPick.selected().value
		if m.method == config.AuthNone {
			m.values.auth = config.Auth{}
			return m, m.enter(stepProbe)
		}
		return m, m.enter(stepAuthDetails)
	case stepAuthDetails:
		cmd := m.authForm.Update(k)
		if m.authForm.Completed() {
			m.values.auth = assembleAuth(m.method, m.authForm.Values())
			return m, m.enter(stepProbe)
		}
		return m, cmd
	case stepProbe:
		if k.String() == "enter" && m.probed {
			return m, m.enter(stepStore)
		}
	case stepStore:
		return m.onStoreKey(k)
	case stepModel:
		return m.onModelKey(k)
	case stepReview:
		if k.String() == "enter" {
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *wizardModel) onStoreKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.storeManual {
		cmd := m.storeForm.Update(k)
		if m.storeForm.Completed() {
			m.values.storeID = strings.TrimSpace(m.storeForm.Values()[0])
			return m, m.afterStore()
		}
		return m, cmd
	}
	cmd, done := m.pickerKey(m.storePick, k)
	if !done {
		return m, cmd
	}
	switch sel := m.storePick.selected(); sel.value {
	case valManual:
		m.storeManual = true
		return m, m.storeForm.Init()
	case valNone:
		m.values.storeID = ""
		return m, m.enter(stepReview) // no store → nothing to pin a model against
	default:
		m.values.storeID = sel.value
		return m, m.afterStore()
	}
}

func (m *wizardModel) onModelKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.modelLoading {
		return m, nil
	}
	if m.modelManual {
		cmd := m.modelForm.Update(k)
		if m.modelForm.Completed() {
			m.values.modelID = strings.TrimSpace(m.modelForm.Values()[0])
			return m, m.enter(stepReview)
		}
		return m, cmd
	}
	cmd, done := m.pickerKey(m.modelPick, k)
	if !done {
		return m, cmd
	}
	switch sel := m.modelPick.selected(); sel.value {
	case valManual:
		m.modelManual = true
		return m, m.modelForm.Init()
	default:
		// valNone ("") means "always latest"; a concrete ID pins that model.
		m.values.modelID = sel.value
		return m, m.enter(stepReview)
	}
}

// afterStore moves on from a chosen store: list its models when connected,
// otherwise fall through to review (offline setups pin manually or not at all).
func (m *wizardModel) afterStore() tea.Cmd {
	if m.values.storeID == "" {
		return m.enter(stepReview)
	}
	if m.connErr != nil {
		// No live connection: let the user type a model id, or skip.
		return m.enter(stepModel)
	}
	return m.enter(stepModel)
}

// enter transitions to step s and returns the command that starts it.
func (m *wizardModel) enter(s step) tea.Cmd {
	m.step = s
	switch s {
	case stepConnection:
		return m.connForm.Init()

	case stepAuthDetails:
		m.authForm = buildAuthForm(m.method)
		m.applyWidth()
		return m.authForm.Init()

	case stepProbe:
		m.probed, m.connErr, m.capped = false, nil, false
		m.stores = nil
		return tea.Batch(m.spin.Tick, m.probeStores())

	case stepStore:
		m.buildStoreStep()
		if m.storeManual {
			return m.storeForm.Init()
		}
		return nil

	case stepModel:
		m.modelManual = false
		m.modelForm = field.NewForm(
			field.New("Authorization Model ID", "leave blank for latest"),
		)
		if m.seed.modelID != "" {
			m.modelForm.SetValues([]string{m.seed.modelID})
		}
		m.applyWidth()
		if m.connErr != nil {
			// Can't list models offline; go straight to manual entry.
			m.modelManual = true
			return m.modelForm.Init()
		}
		m.modelLoading = true
		m.models, m.modelErr = nil, nil
		return tea.Batch(m.spin.Tick, m.probeModels(m.values.storeID))
	}
	return nil
}

// back steps one screen toward the start; on the welcome screen it cancels.
func (m *wizardModel) back() tea.Cmd {
	switch m.step {
	case stepWelcome:
		m.cancelled = true
		return tea.Quit
	case stepConnection:
		m.step = stepWelcome
	case stepAuthMethod:
		return m.enter(stepConnection)
	case stepAuthDetails:
		m.step = stepAuthMethod
	case stepProbe:
		// Back from the probe returns to auth (its method chooser or details).
		if m.method == "" || m.method == config.AuthNone {
			m.step = stepAuthMethod
		} else {
			return m.enter(stepAuthDetails)
		}
	case stepStore:
		if m.storeManual {
			m.storeManual = false // back out of manual entry into the picker
			return nil
		}
		return m.enter(stepProbe)
	case stepModel:
		if m.modelManual && m.connErr == nil {
			m.modelManual = false
			return nil
		}
		return m.enter(stepStore)
	case stepReview:
		// Return to wherever review was reached from.
		if m.values.storeID == "" {
			return m.enter(stepStore)
		}
		return m.enter(stepModel)
	}
	return nil
}

// pickerKey routes a key to a picker, reporting whether it was selected.
func (m *wizardModel) pickerKey(p *picker, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "up", "k":
		p.move(-1)
	case "down", "j":
		p.move(1)
	case "enter", " ":
		return nil, true
	}
	return nil, false
}

// --- async command builders ---

func (m *wizardModel) probeStores() tea.Cmd {
	pr := m.newProber(m.values.apiURL, m.values.auth, m.timeout)
	ctx := m.ctx
	return func() tea.Msg {
		stores, err := pr.stores(ctx)
		capped := false
		if len(stores) > storeProbeCap {
			stores, capped = stores[:storeProbeCap], true
		}
		return storesMsg{stores: stores, capped: capped, err: err}
	}
}

func (m *wizardModel) probeModels(storeID string) tea.Cmd {
	pr := m.newProber(m.values.apiURL, m.values.auth, m.timeout)
	ctx := m.ctx
	return func() tea.Msg {
		models, err := pr.models(ctx, storeID)
		if len(models) > modelProbeCap {
			models = models[:modelProbeCap]
		}
		return modelsMsg{storeID: storeID, models: models, err: err}
	}
}

// --- step builders ---

func (m *wizardModel) buildStoreStep() {
	m.storeManual = false
	m.storeForm = field.NewForm(field.New("Store ID", "leave blank to skip"))
	if m.seed.storeID != "" {
		m.storeForm.SetValues([]string{m.seed.storeID})
	}
	if m.connErr != nil || len(m.stores) == 0 {
		// Nothing to pick from — go straight to manual entry.
		m.storeManual = true
		m.applyWidth()
		return
	}
	items := make([]pickItem, 0, len(m.stores)+2)
	for _, s := range m.stores {
		items = append(items, pickItem{title: s.Name, desc: s.ID, value: s.ID})
	}
	items = append(items,
		pickItem{title: "Enter a store ID manually", value: valManual},
		pickItem{title: "Skip — no store yet", value: valNone},
	)
	m.storePick = newPicker(items)
	// Pre-select a store matching a --store-id flag, if it was listed.
	if m.seed.storeID != "" {
		for i, it := range items {
			if it.value == m.seed.storeID {
				m.storePick.cursor = i
			}
		}
	}
	m.applyWidth()
}

func (m *wizardModel) buildModelPicker() {
	items := []pickItem{
		{title: "Always use the latest model", desc: "don't pin; follow the store's newest model", value: valNone},
	}
	for i, mod := range m.models {
		desc := mod.SchemaVersion
		if i == 0 {
			desc = "latest · schema " + mod.SchemaVersion
		}
		items = append(items, pickItem{title: mod.ID, desc: desc, value: mod.ID})
	}
	items = append(items, pickItem{title: "Enter a model ID manually", value: valManual})
	m.modelPick = newPicker(items)
	if m.seed.modelID != "" {
		for i, it := range items {
			if it.value == m.seed.modelID {
				m.modelPick.cursor = i
			}
		}
	}
	m.applyWidth()
}

// contentWidth is the inner width available to a step's content, clamped so the
// card stays readable on both narrow and very wide terminals.
func (m *wizardModel) contentWidth() int {
	w := m.width - 12
	if w > 64 {
		w = 64
	}
	if w < 32 {
		w = 32
	}
	return w
}

// applyWidth sizes every active form to the card's content width.
func (m *wizardModel) applyWidth() {
	w := m.contentWidth()
	for _, f := range []*field.Form{m.connForm, m.authForm, m.storeForm, m.modelForm} {
		if f != nil {
			f.SetWidth(w)
			f.SetHighlight(style.BgHighlight)
		}
	}
}

// --- real prober ---

type clientProber struct {
	apiURL  string
	auth    config.Auth
	timeout time.Duration
}

func newClientProber(apiURL string, auth config.Auth, timeout time.Duration) prober {
	return clientProber{apiURL: apiURL, auth: auth, timeout: timeout}
}

func (p clientProber) client(storeID string) (*openfga.Client, error) {
	return client.New(
		config.Resolved{APIURL: p.apiURL, Auth: p.auth, StoreID: storeID},
		client.WithTimeout(p.timeout),
	)
}

func (p clientProber) stores(ctx context.Context) ([]openfga.Store, error) {
	cl, err := p.client("")
	if err != nil {
		return nil, err
	}
	var out []openfga.Store
	for st, err := range cl.Stores.All(ctx, nil) {
		if err != nil {
			return out, err
		}
		out = append(out, st)
		if len(out) > storeProbeCap {
			break
		}
	}
	return out, nil
}

func (p clientProber) models(ctx context.Context, storeID string) ([]openfga.AuthorizationModel, error) {
	cl, err := p.client(storeID)
	if err != nil {
		return nil, err
	}
	var out []openfga.AuthorizationModel
	for mod, err := range cl.AuthorizationModels.All(ctx, nil, openfga.WithStore(storeID)) {
		if err != nil {
			return out, err
		}
		out = append(out, mod)
		if len(out) > modelProbeCap {
			break
		}
	}
	return out, nil
}

// --- auth form assembly ---

func newAuthPicker() *picker {
	return newPicker([]pickItem{
		{title: "API token", desc: "a static bearer token (FGA_API_TOKEN)", value: config.AuthAPIToken},
		{title: "OAuth2 client credentials", desc: "client id + secret exchanged for a token", value: config.AuthClientCredentials},
		{title: "Private-key JWT", desc: "signed assertion (client id + PEM key)", value: config.AuthPrivateKeyJWT},
		{title: "None", desc: "no authentication (local / dev servers)", value: config.AuthNone},
	})
}

// buildAuthForm returns the secret-field form for a method (never called for none).
func buildAuthForm(method string) *field.Form {
	switch method {
	case config.AuthAPIToken:
		return field.NewForm(
			field.New("API token", "required").Secret().WithValidate(required("an API token")),
		)
	case config.AuthClientCredentials:
		return field.NewForm(
			field.New("Client ID", "required").WithValidate(required("a client id")),
			field.New("Client secret", "required").Secret().WithValidate(required("a client secret")),
			field.New("Token URL", "https://issuer/oauth/token").WithValidate(validateURL),
			field.New("API audience", "optional"),
			field.New("Scopes", "optional, space-separated"),
		)
	case config.AuthPrivateKeyJWT:
		return field.NewForm(
			field.New("Client ID", "required").WithValidate(required("a client id")),
			field.New("Token URL", "https://issuer/oauth/token").WithValidate(validateURL),
			field.New("Key file", "path to a PEM signing key").WithValidate(required("a key file path")),
			field.New("API audience", "optional"),
		)
	}
	return field.NewForm()
}

// assembleAuth folds a form's values back into an Auth, in the field order
// buildAuthForm laid out.
func assembleAuth(method string, vals []string) config.Auth {
	get := func(i int) string {
		if i < len(vals) {
			return strings.TrimSpace(vals[i])
		}
		return ""
	}
	switch method {
	case config.AuthAPIToken:
		return config.Auth{Method: config.AuthAPIToken, Token: get(0)}
	case config.AuthClientCredentials:
		return config.Auth{
			Method:       config.AuthClientCredentials,
			ClientID:     get(0),
			ClientSecret: get(1),
			TokenURL:     get(2),
			Audience:     get(3),
			Scopes:       splitScopes(get(4)),
		}
	case config.AuthPrivateKeyJWT:
		return config.Auth{
			Method:      config.AuthPrivateKeyJWT,
			ClientID:    get(0),
			TokenURL:    get(1),
			KeyFile:     get(2),
			APIAudience: get(3),
		}
	}
	return config.Auth{}
}

func splitScopes(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f
}

// --- field validators ---

func required(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter " + what)
		}
		return nil
	}
}

func validateURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("enter a URL")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an http(s) URL")
	}
	return nil
}
