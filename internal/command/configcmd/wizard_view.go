package configcmd

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/logo"
)

// --- picker: a small vertical single-select list with descriptions ---

type pickItem struct {
	title string
	desc  string
	value string
}

type picker struct {
	items  []pickItem
	cursor int
}

func newPicker(items []pickItem) *picker { return &picker{items: items} }

func (p *picker) move(d int) {
	if len(p.items) == 0 {
		return
	}
	p.cursor = (p.cursor + d + len(p.items)) % len(p.items)
}

func (p *picker) selected() pickItem {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return pickItem{}
	}
	return p.items[p.cursor]
}

func (p *picker) view(width int) string {
	var b strings.Builder
	for i, it := range p.items {
		if i > 0 {
			b.WriteString("\n")
		}
		if i == p.cursor {
			b.WriteString(lipgloss.NewStyle().Foreground(style.Primary).Render("▌ "))
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Fg).Render(it.title))
		} else {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(style.Muted).Render(it.title))
		}
		if it.desc != "" {
			b.WriteString("\n    " + lipgloss.NewStyle().Foreground(style.Faintc).Render(clamp(it.desc, width-4)))
		}
	}
	return b.String()
}

func clamp(s string, w int) string {
	r := []rune(s)
	if w < 1 || len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// --- view ---

// stepInfo is the header copy for each phase of the tour.
type stepInfo struct {
	phase    int // 0-based index into tourPhases, -1 for the welcome screen
	title    string
	subtitle string
}

var tourPhases = []string{"Connect", "Auth", "Test", "Store", "Model", "Review"}

func (m *wizardModel) info() stepInfo {
	switch m.step {
	case stepWelcome:
		return stepInfo{-1, "Welcome", "Let's connect the CLI to an OpenFGA server."}
	case stepConnection:
		return stepInfo{0, "Server", "Where does your OpenFGA API live?"}
	case stepAuthMethod:
		return stepInfo{1, "Authentication", "How should the CLI authenticate?"}
	case stepAuthDetails:
		return stepInfo{1, "Authentication", authTitle(m.method)}
	case stepProbe:
		return stepInfo{2, "Connection test", "Checking we can reach the server."}
	case stepStore:
		return stepInfo{3, "Store", "Pick the store to work in."}
	case stepModel:
		return stepInfo{4, "Model", "Pick an authorization model."}
	case stepReview:
		return stepInfo{5, "Review", "Save this profile?"}
	}
	return stepInfo{}
}

func authTitle(method string) string {
	switch method {
	case config.AuthAPIToken:
		return "Enter your API token."
	case config.AuthClientCredentials:
		return "Enter your OAuth2 client credentials."
	case config.AuthPrivateKeyJWT:
		return "Enter your private-key JWT details."
	}
	return ""
}

func (m *wizardModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	return v
}

func (m *wizardModel) viewString() string {
	if m.width < 44 || m.height < 16 {
		return lipgloss.Place(max(m.width, 1), max(m.height, 1),
			lipgloss.Center, lipgloss.Center,
			style.Faint.Render("terminal too small — need at least 44×16"))
	}

	info := m.info()
	cw := m.contentWidth()

	var b strings.Builder
	b.WriteString(logo.Wordmark(-1))
	b.WriteString("\n\n")
	b.WriteString(style.Title.Render(info.title))
	if m.seed.profile != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(style.Faintc).Render("   profile: " + m.seed.profile))
	}
	b.WriteString("\n")
	b.WriteString(style.Subtitle.Render(info.subtitle))
	b.WriteString("\n\n")
	b.WriteString(m.stepBody(cw))

	// Width = content (cw) + 2 cols of horizontal padding each side, so the
	// border stays a fixed size across steps regardless of their content.
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Subtle).
		Padding(1, 2).
		Width(cw + 4).
		Render(b.String())

	footer := m.footer(info)

	block := card + "\n" + footer
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

// stepBody renders the interactive content for the current step.
func (m *wizardModel) stepBody(cw int) string {
	switch m.step {
	case stepWelcome:
		body := lipgloss.NewStyle().Foreground(style.Muted).Width(cw).Render(
			"This quick tour sets up a connection profile: the server address, how " +
				"to authenticate, and an optional default store and model.\n\nAnswers are " +
				"saved to your CLI config. Secrets go to the OS keyring where available.")
		if m.seed.overwrite {
			warn := lipgloss.NewStyle().Foreground(style.Amber).Width(cw).Render(
				"⚠ Profile \"" + m.seed.profile + "\" already exists — finishing will overwrite it.")
			return warn + "\n\n" + body
		}
		return body
	case stepConnection:
		return m.connForm.View()
	case stepAuthMethod:
		return m.authPick.view(cw)
	case stepAuthDetails:
		return m.authForm.View()
	case stepProbe:
		return m.probeBody()
	case stepStore:
		return m.storeBody(cw)
	case stepModel:
		return m.modelBody(cw)
	case stepReview:
		return m.reviewBody(cw)
	}
	return ""
}

func (m *wizardModel) probeBody() string {
	host := m.values.apiURL
	if !m.probed {
		return m.spin.View() + " " + style.Value.Render("Connecting to "+host+" …")
	}
	if m.connErr != nil {
		return style.Failure.Render("✗ Couldn't connect") + "\n" +
			lipgloss.NewStyle().Foreground(style.Muted).Render(clamp(m.connErr.Error(), 60)) + "\n\n" +
			style.Warn.Render("That's OK — you can finish setup and fix the connection later.")
	}
	n := len(m.stores)
	msg := fmt.Sprintf("✓ Connected · %d store%s found", n, plural(n))
	if m.capped {
		msg = fmt.Sprintf("✓ Connected · showing first %d stores", n)
	}
	return style.Success.Render(msg)
}

func (m *wizardModel) storeBody(cw int) string {
	if m.storeManual {
		var b strings.Builder
		if m.connErr != nil {
			b.WriteString(style.Warn.Render("Not connected — enter a store ID or leave blank.") + "\n\n")
		}
		b.WriteString(m.storeForm.View())
		return b.String()
	}
	head := lipgloss.NewStyle().Foreground(style.Faintc).Render(
		fmt.Sprintf("%d store%s available", len(m.stores), plural(len(m.stores))))
	return head + "\n\n" + m.storePick.view(cw)
}

func (m *wizardModel) modelBody(cw int) string {
	if m.modelLoading {
		return m.spin.View() + " " + style.Value.Render("Loading models …")
	}
	if m.modelManual {
		var b strings.Builder
		if m.modelErr != nil {
			b.WriteString(style.Warn.Render("Couldn't list models — enter an ID or leave blank.") + "\n\n")
		}
		b.WriteString(m.modelForm.View())
		return b.String()
	}
	if len(m.models) == 0 {
		return lipgloss.NewStyle().Foreground(style.Muted).Render("This store has no models yet.") +
			"\n\n" + m.modelPick.view(cw)
	}
	return m.modelPick.view(cw)
}

func (m *wizardModel) reviewBody(cw int) string {
	rows := [][2]string{
		{"API URL", m.values.apiURL},
		{"Auth", authSummary(m.values.auth)},
		{"Store", m.storeLabel()},
		{"Model", modelSummary(m.values.modelID)},
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		key := lipgloss.NewStyle().Foreground(style.Muted).Width(10).Render(r[0])
		b.WriteString(key + "  " + style.Value.Render(clamp(r[1], cw-12)))
	}
	if m.seed.overwrite {
		b.WriteString("\n\n" + lipgloss.NewStyle().Foreground(style.Amber).Width(cw).Render(
			"⚠ This replaces the existing \""+m.seed.profile+"\" profile."))
	}
	return b.String()
}

func (m *wizardModel) footer(info stepInfo) string {
	dots := m.dots(info.phase)
	keys := m.keys()
	gap := m.contentWidth() + 2 - lipgloss.Width(dots) - lipgloss.Width(keys)
	if gap < 2 {
		gap = 2
	}
	return "  " + dots + strings.Repeat(" ", gap) + keys
}

func (m *wizardModel) dots(active int) string {
	if active < 0 {
		return lipgloss.NewStyle().Foreground(style.Faintc).Render("press enter to begin")
	}
	var parts []string
	for i := range tourPhases {
		switch {
		case i == active:
			parts = append(parts, lipgloss.NewStyle().Foreground(style.Primary).Render("●"))
		case i < active:
			parts = append(parts, lipgloss.NewStyle().Foreground(style.Muted).Render("●"))
		default:
			parts = append(parts, lipgloss.NewStyle().Foreground(style.Faintc).Render("○"))
		}
	}
	label := lipgloss.NewStyle().Foreground(style.Faintc).Render(
		fmt.Sprintf(" %d of %d", active+1, len(tourPhases)))
	return strings.Join(parts, " ") + label
}

func (m *wizardModel) keys() string {
	kc := func(s string) string {
		return lipgloss.NewStyle().Foreground(style.Muted).Render(s)
	}
	switch m.step {
	case stepWelcome:
		return kc("enter") + " begin  " + kc("esc") + " cancel"
	case stepAuthMethod, stepStore, stepModel:
		if (m.step == stepStore && m.storeManual) || (m.step == stepModel && m.modelManual) {
			return kc("enter") + " next  " + kc("esc") + " back"
		}
		if m.step == stepModel && m.modelLoading {
			return kc("esc") + " back"
		}
		return kc("↑↓") + " move  " + kc("enter") + " select  " + kc("esc") + " back"
	case stepProbe:
		if !m.probed {
			return kc("esc") + " cancel"
		}
		return kc("enter") + " continue  " + kc("esc") + " back"
	case stepReview:
		if m.seed.overwrite {
			return kc("enter") + " overwrite  " + kc("esc") + " back"
		}
		return kc("enter") + " save  " + kc("esc") + " back"
	default:
		return kc("enter") + " next  " + kc("esc") + " back"
	}
}

// --- small helpers ---

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// storeLabel renders the chosen store for review, adding its name when the ID
// came from the fetched list.
func (m *wizardModel) storeLabel() string {
	if m.values.storeID == "" {
		return "—"
	}
	for _, s := range m.stores {
		if s.ID == m.values.storeID && s.Name != "" {
			return s.Name + " · " + s.ID
		}
	}
	return m.values.storeID
}

func authSummary(a config.Auth) string {
	switch a.Method {
	case config.AuthAPIToken:
		return "API token"
	case config.AuthClientCredentials:
		return "OAuth2 client credentials"
	case config.AuthPrivateKeyJWT:
		return "Private-key JWT"
	default:
		return "None"
	}
}

func modelSummary(id string) string {
	if id == "" {
		return "latest"
	}
	return id
}
