package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
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
	lines      []string
	status     string
	errMessage string
	turnActive bool
	assistant  string
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
		lines:      []string{"Nami Bubble Tea shell starting..."},
		status:     "starting",
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
		m.status = "engine starting"
	case engineEventMsg:
		m.applyEngineEvent(msg.event)
		return m, m.engine.wait()
	case engineDoneMsg:
		if msg.err != nil && msg.err != context.Canceled {
			m.errMessage = msg.err.Error()
			m.appendTranscriptLine("engine stopped: " + msg.err.Error())
		} else {
			m.appendTranscriptLine("engine stopped")
		}
		m.status = "stopped"
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keymap.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.resize()
		case key.Matches(msg, m.keymap.Cancel):
			if m.turnActive {
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
				m.errMessage = err.Error()
				return m, nil
			}
			m.appendTranscriptLine("> " + text)
			m.assistant = ""
			m.turnActive = true
			m.status = "running"
			m.prompt.Reset()
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

func (m *model) applyEngineEvent(event ipc.StreamEvent) {
	switch event.Type {
	case ipc.EventReady:
		m.status = "ready"
	case ipc.EventError:
		m.errMessage = summarizeEvent(event)
	case ipc.EventTokenDelta:
		m.applyAssistantDelta(summarizeEvent(event))
		return
	case ipc.EventTurnComplete:
		m.turnActive = false
		m.status = "ready"
	}
	if summary := summarizeEvent(event); strings.TrimSpace(summary) != "" {
		m.appendTranscriptLine(summary)
	}
}

func (m *model) appendTranscriptLine(line string) {
	m.lines = append(m.lines, line)
	m.renderTranscript()
}

func (m *model) applyAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	if m.assistant == "" {
		m.lines = append(m.lines, "assistant: ")
	}
	m.assistant += delta
	m.lines[len(m.lines)-1] = "assistant: " + strings.TrimRight(m.assistant, "\n")
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
	if strings.TrimSpace(m.errMessage) != "" {
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
	m.transcript.SetContent(strings.Join(m.lines, "\n"))
	m.transcript.GotoBottom()
}

func (m model) content() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	status := statusStyle.Width(width).Render("nami | bubble tea | " + m.status)
	footer := footerStyle.Width(width).Render(m.help.View(m.keymap))
	parts := []string{
		status,
		m.transcript.View(),
		m.prompt.View(),
	}
	if strings.TrimSpace(m.errMessage) != "" {
		parts = append(parts, errorStyle.Width(width).Render(m.errMessage))
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
