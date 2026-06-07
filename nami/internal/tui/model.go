package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/channyeintun/nami/internal/config"
)

type model struct {
	cfg        config.Config
	engine     engineClient
	keymap     keyMap
	help       help.Model
	width      int
	height     int
	transcript viewport.Model
	prompt     textarea.Model
	state      uiState
}

func newModel(ctx context.Context, cfg config.Config) model {
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
		engine:     newEngineClient(ctx),
		keymap:     defaultKeyMap(),
		help:       help.New(),
		transcript: transcript,
		prompt:     prompt,
		state:      newUIState(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.engine.start(m.cfg), m.engine.wait())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case engineStartedMsg:
		m.state.Status = "engine starting"
	case engineEventMsg:
		m.state = applyEvent(m.state, msg.event)
		m.renderTranscript()
		return m, m.engine.wait()
	case engineDoneMsg:
		if msg.err != nil && msg.err != context.Canceled {
			m.state = m.state.stopEngine(msg.err)
		} else {
			m.state = m.state.stopEngine(nil)
		}
		m.renderTranscript()
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keymap.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.resize()
		case key.Matches(msg, m.keymap.Cancel):
			if m.state.TurnActive {
				m.appendTranscriptLine("cancel requested")
				return m, m.engine.cancelTurn()
			}
			return m, tea.Batch(m.engine.shutdown(), tea.Quit)
		case key.Matches(msg, m.keymap.Quit):
			return m, tea.Batch(m.engine.shutdown(), tea.Quit)
		case key.Matches(msg, m.keymap.Submit):
			text := strings.TrimSpace(m.prompt.Value())
			if text == "" {
				break
			}
			msg, err := makeUserInputMessage(text)
			if err != nil {
				m.state.ErrorMessage = err.Error()
				return m, nil
			}
			m.state = m.state.startTurn(text)
			m.prompt.Reset()
			m.renderTranscript()
			return m, m.engine.send(msg)
		}
	}

	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	cmds = append(cmds, cmd)
	m.transcript, cmd = m.transcript.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) appendTranscriptLine(line string) {
	m.state = m.state.appendLine(line)
	m.renderTranscript()
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

	statusHeight := 1
	promptHeight := promptHeightFor(m.height)
	footerHeight := lipgloss.Height(m.help.View(m.keymap))
	if footerHeight < 1 {
		footerHeight = 1
	}
	errorHeight := 0
	if strings.TrimSpace(m.state.ErrorMessage) != "" {
		errorHeight = 1
	}
	transcriptHeight := m.height - promptHeight - statusHeight - footerHeight - errorHeight
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}

	m.transcript.SetWidth(m.width)
	m.transcript.SetHeight(transcriptHeight)
	m.prompt.SetWidth(m.width)
	m.prompt.SetHeight(promptHeight)
	m.help.SetWidth(m.width)
	m.renderTranscript()
}

func promptHeightFor(totalHeight int) int {
	switch {
	case totalHeight < 10:
		return 1
	case totalHeight < 18:
		return 2
	default:
		return 3
	}
}

func (m *model) renderTranscript() {
	m.transcript.SetContent(strings.Join(m.state.Lines, "\n"))
	m.transcript.GotoBottom()
}

func (m model) content() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	status := statusStyle.Width(width).Render(m.statusLine())
	footer := footerStyle.Width(width).Render(m.help.View(m.keymap))
	parts := []string{
		status,
		m.transcript.View(),
		m.prompt.View(),
	}
	if strings.TrimSpace(m.state.ErrorMessage) != "" {
		parts = append(parts, errorStyle.Width(width).Render(m.state.ErrorMessage))
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m model) statusLine() string {
	parts := []string{"nami", m.state.Status}
	if m.state.Mode != "" {
		parts = append(parts, "mode "+m.state.Mode)
	}
	if m.state.Model != "" {
		model := "model " + m.state.Model
		if m.state.Reasoning != "" {
			model += " " + m.state.Reasoning
		}
		parts = append(parts, model)
	}
	if m.state.ContextMax > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d/%d", m.state.ContextUsage, m.state.ContextMax))
	} else if m.state.ContextUsage > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", m.state.ContextUsage))
	}
	if m.state.TotalUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", m.state.TotalUSD))
	}
	if m.state.RateLimit != "" {
		parts = append(parts, "limit "+m.state.RateLimit)
	}
	if len(m.state.Artifacts) > 0 {
		parts = append(parts, fmt.Sprintf("artifacts %d", len(m.state.Artifacts)))
	}
	if len(m.state.BackgroundCommands) > 0 {
		parts = append(parts, fmt.Sprintf("bg cmd %d", len(m.state.BackgroundCommands)))
	}
	if len(m.state.BackgroundAgents) > 0 {
		parts = append(parts, fmt.Sprintf("agents %d", len(m.state.BackgroundAgents)))
	}
	if m.state.ArtifactReview != nil {
		parts = append(parts, "review "+m.state.ArtifactReview.Artifact.Title)
	}
	return strings.Join(parts, " | ")
}
