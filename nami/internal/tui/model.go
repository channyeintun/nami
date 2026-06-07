package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/channyeintun/nami/internal/config"
)

type model struct {
	cfg        config.Config
	width      int
	height     int
	transcript viewport.Model
	prompt     textarea.Model
	lines      []string
	status     string
	errMessage string
}

func newModel(cfg config.Config) model {
	prompt := textarea.New()
	prompt.Placeholder = "Ask Nami"
	prompt.Prompt = "> "
	prompt.ShowLineNumbers = false
	prompt.SetHeight(3)
	prompt.SetWidth(80)
	_ = prompt.Focus()

	transcript := viewport.New()
	transcript.SoftWrap = true
	transcript.SetContent("Nami Bubble Tea shell starting...")

	return model{
		cfg:        cfg,
		transcript: transcript,
		prompt:     prompt,
		lines:      []string{"Nami Bubble Tea shell starting..."},
		status:     "starting",
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			text := strings.TrimSpace(m.prompt.Value())
			if text == "" {
				break
			}
			m.lines = append(m.lines, "> "+text)
			m.prompt.Reset()
			m.renderTranscript()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	cmds = append(cmds, cmd)
	m.transcript, cmd = m.transcript.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	var view tea.View
	view.AltScreen = true
	view.SetContent(m.content())
	return view
}

func (m *model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	promptHeight := 3
	statusHeight := 1
	footerHeight := 1
	transcriptHeight := m.height - promptHeight - statusHeight - footerHeight
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}

	m.transcript.SetWidth(m.width)
	m.transcript.SetHeight(transcriptHeight)
	m.prompt.SetWidth(m.width)
	m.prompt.SetHeight(promptHeight)
	m.renderTranscript()
}

func (m *model) renderTranscript() {
	m.transcript.SetContent(strings.Join(m.lines, "\n"))
	m.transcript.GotoBottom()
}

func (m model) content() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	status := statusStyle.Width(width).Render("nami | bubble tea | " + m.status)
	footer := footerStyle.Width(width).Render("enter send  esc quit  ctrl+c quit")
	parts := []string{
		status,
		m.transcript.View(),
		m.prompt.View(),
		footer,
	}
	if strings.TrimSpace(m.errMessage) != "" {
		parts = append(parts, errorStyle.Width(width).Render(m.errMessage))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
