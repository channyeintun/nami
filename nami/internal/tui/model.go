package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/channyeintun/nami/internal/config"
)

type model struct {
	cfg                config.Config
	engine             engineClient
	keymap             keyMap
	help               help.Model
	width              int
	height             int
	transcript         viewport.Model
	selection          list.Model
	prompt             textarea.Model
	state              uiState
	promptHistory      []string
	promptHistoryIndex int
	followTail         bool
	searchActive       bool
	searchQuery        string
	searchMatches      int
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
	selection := list.New(nil, list.NewDefaultDelegate(), 80, 5)
	selection.SetShowStatusBar(false)
	selection.SetFilteringEnabled(false)
	selection.SetShowHelp(false)

	return model{
		cfg:                cfg,
		engine:             newEngineClient(ctx),
		keymap:             defaultKeyMap(),
		help:               help.New(),
		transcript:         transcript,
		selection:          selection,
		prompt:             prompt,
		state:              newUIState(),
		promptHistoryIndex: -1,
		followTail:         true,
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
		hadSelection := m.state.SelectionRequest
		m.state = applyEvent(m.state, msg.event)
		if m.state.SelectionRequest != hadSelection {
			m.syncSelectionList()
		}
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
		if m.searchActive {
			m.handleSearchKey(msg)
			return m, nil
		}
		if hasActionableDialog(m.state) {
			if handled, cmd := m.handleDialogKey(msg); handled {
				return m, cmd
			}
		}
		if m.state.SelectionRequest != nil {
			if handled, cmd := m.handleSelectionKey(msg); handled {
				return m, cmd
			}
		}
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
		case key.Matches(msg, m.keymap.Complete):
			m.completeSlashCommand()
			return m, nil
		case key.Matches(msg, m.keymap.HistoryPrev):
			m.previousPrompt()
			return m, nil
		case key.Matches(msg, m.keymap.HistoryNext):
			m.nextPrompt()
			return m, nil
		case key.Matches(msg, m.keymap.Search):
			m.searchActive = true
			m.updateSearchMatches()
			return m, nil
		case key.Matches(msg, m.keymap.PageUp):
			m.followTail = false
			m.transcript.PageUp()
			return m, nil
		case key.Matches(msg, m.keymap.PageDown):
			m.transcript.PageDown()
			m.followTail = m.transcript.AtBottom()
			return m, nil
		case key.Matches(msg, m.keymap.FollowTail):
			m.followTail = true
			m.transcript.GotoBottom()
			return m, nil
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
			m.recordPromptHistory(text)
			m.prompt.Reset()
			m.renderTranscript()
			return m, m.engine.send(msg)
		}
	}

	var cmd tea.Cmd
	if m.state.SelectionRequest != nil {
		m.selection, cmd = m.selection.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.prompt, cmd = m.prompt.Update(msg)
	cmds = append(cmds, cmd)
	m.transcript, cmd = m.transcript.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) syncSelectionList() {
	if m.state.SelectionRequest == nil {
		m.selection.SetItems(nil)
		return
	}
	_ = m.selection.SetItems(selectionItems(m.state.SelectionRequest.Options))
	m.selection.Title = selectionListTitle(*m.state.SelectionRequest)
}

func (m *model) handleSelectionKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keymap.Submit):
		return true, m.sendSelectionResponse()
	case key.Matches(msg, m.keymap.Quit), key.Matches(msg, m.keymap.Deny):
		return true, m.sendSelectionCancel()
	default:
		return false, nil
	}
}

func (m *model) sendSelectionResponse() tea.Cmd {
	if m.state.SelectionRequest == nil {
		return nil
	}
	item, ok := m.selection.SelectedItem().(selectionOptionState)
	if !ok {
		m.state.ErrorMessage = "no selection item is active"
		return nil
	}
	msg, err := makeSelectionResponseMessage(*m.state.SelectionRequest, item, false)
	if err != nil {
		m.state.ErrorMessage = err.Error()
		return nil
	}
	m.state = m.state.clearSelectionRequest()
	m.syncSelectionList()
	return m.engine.send(msg)
}

func (m *model) sendSelectionCancel() tea.Cmd {
	if m.state.SelectionRequest == nil {
		return nil
	}
	msg, err := makeSelectionResponseMessage(*m.state.SelectionRequest, selectionOptionState{}, true)
	if err != nil {
		m.state.ErrorMessage = err.Error()
		return nil
	}
	m.state = m.state.clearSelectionRequest()
	m.syncSelectionList()
	return m.engine.send(msg)
}

func (m *model) handleDialogKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if request := m.state.PermissionRequest; request != nil {
		switch {
		case key.Matches(msg, m.keymap.Approve):
			return true, m.sendPermissionResponse(request.RequestID, "allow")
		case key.Matches(msg, m.keymap.Always):
			return true, m.sendPermissionResponse(request.RequestID, "always_allow")
		case key.Matches(msg, m.keymap.Deny), key.Matches(msg, m.keymap.Quit):
			return true, m.sendPermissionResponse(request.RequestID, "deny")
		}
	}
	if review := m.state.ArtifactReview; review != nil {
		switch {
		case key.Matches(msg, m.keymap.Approve):
			return true, m.sendArtifactReviewResponse(review.RequestID, "approve")
		case key.Matches(msg, m.keymap.Revise):
			return true, m.sendArtifactReviewResponse(review.RequestID, "revise")
		case key.Matches(msg, m.keymap.Deny), key.Matches(msg, m.keymap.Quit):
			return true, m.sendArtifactReviewResponse(review.RequestID, "cancel")
		}
	}
	return false, nil
}

func (m *model) sendPermissionResponse(requestID, decision string) tea.Cmd {
	msg, err := makePermissionResponseMessage(requestID, decision)
	if err != nil {
		m.state.ErrorMessage = err.Error()
		return nil
	}
	m.state = m.state.clearPermissionRequest()
	return m.engine.send(msg)
}

func (m *model) sendArtifactReviewResponse(requestID, decision string) tea.Cmd {
	msg, err := makeArtifactReviewResponseMessage(requestID, decision)
	if err != nil {
		m.state.ErrorMessage = err.Error()
		return nil
	}
	m.state = m.state.clearArtifactReview()
	return m.engine.send(msg)
}

func (m *model) recordPromptHistory(text string) {
	if text == "" {
		return
	}
	if len(m.promptHistory) == 0 || m.promptHistory[len(m.promptHistory)-1] != text {
		m.promptHistory = append(m.promptHistory, text)
	}
	m.promptHistoryIndex = len(m.promptHistory)
}

func (m *model) previousPrompt() {
	if len(m.promptHistory) == 0 {
		return
	}
	if m.promptHistoryIndex < 0 || m.promptHistoryIndex > len(m.promptHistory) {
		m.promptHistoryIndex = len(m.promptHistory)
	}
	if m.promptHistoryIndex > 0 {
		m.promptHistoryIndex--
	}
	m.prompt.SetValue(m.promptHistory[m.promptHistoryIndex])
}

func (m *model) nextPrompt() {
	if len(m.promptHistory) == 0 || m.promptHistoryIndex < 0 {
		return
	}
	if m.promptHistoryIndex < len(m.promptHistory)-1 {
		m.promptHistoryIndex++
		m.prompt.SetValue(m.promptHistory[m.promptHistoryIndex])
		return
	}
	m.promptHistoryIndex = len(m.promptHistory)
	m.prompt.SetValue("")
}

func (m *model) completeSlashCommand() {
	value := strings.TrimSpace(m.prompt.Value())
	if !strings.HasPrefix(value, "/") {
		return
	}
	prefix := strings.TrimPrefix(value, "/")
	for _, command := range m.state.SlashCommands {
		if strings.HasPrefix(command.Name, prefix) {
			completed := "/" + command.Name
			if command.TakesArguments {
				completed += " "
			}
			m.prompt.SetValue(completed)
			return
		}
	}
}

func (m *model) handleSearchKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "enter", "esc", "ctrl+g":
		m.searchActive = false
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
		}
	default:
		if text := msg.Key().Text; text != "" {
			m.searchQuery += text
		}
	}
	m.updateSearchMatches()
}

func (m *model) updateSearchMatches() {
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if query == "" {
		m.searchMatches = 0
		return
	}
	matches := 0
	for _, entry := range m.state.Transcript {
		if strings.Contains(strings.ToLower(entry.Text), query) {
			matches++
		}
	}
	m.searchMatches = matches
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
	dialogHeight := dialogHeight(m.state, m.width)
	selectionHeight := 0
	if m.state.SelectionRequest != nil {
		selectionHeight = 6
	}
	transcriptHeight := m.height - promptHeight - statusHeight - footerHeight - errorHeight - dialogHeight - selectionHeight
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}

	m.transcript.SetWidth(m.width)
	m.transcript.SetHeight(transcriptHeight)
	m.selection.SetWidth(m.width)
	m.selection.SetHeight(selectionHeight)
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
	m.transcript.SetContent(renderTranscript(m.state.Transcript))
	m.updateSearchMatches()
	if m.followTail {
		m.transcript.GotoBottom()
	}
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
	}
	if dialog := renderDialog(m.state, width); strings.TrimSpace(dialog) != "" {
		parts = append(parts, dialog)
	}
	if m.state.SelectionRequest != nil {
		parts = append(parts, m.selection.View())
	}
	parts = append(parts, m.prompt.View())
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
	if m.state.SessionTitle != "" {
		parts = append(parts, "session "+m.state.SessionTitle)
	} else if m.state.SessionID != "" {
		parts = append(parts, "session "+m.state.SessionID)
	}
	if m.state.MemoryCount > 0 {
		parts = append(parts, fmt.Sprintf("memory %d", m.state.MemoryCount))
	}
	if m.state.RetrievalSummary != "" {
		parts = append(parts, "retrieval "+m.state.RetrievalSummary)
	}
	if m.state.Compacting {
		parts = append(parts, "compacting")
	} else if m.state.CompactSummary != "" {
		parts = append(parts, "compact "+m.state.CompactSummary)
	}
	if m.state.LastTiming != "" {
		parts = append(parts, "timing "+m.state.LastTiming)
	}
	if m.searchActive || m.searchQuery != "" {
		parts = append(parts, fmt.Sprintf("search %q %d", m.searchQuery, m.searchMatches))
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
	if m.state.PermissionRequest != nil {
		parts = append(parts, "permission "+m.state.PermissionRequest.Tool)
	}
	if m.state.QuestionRequest != nil {
		parts = append(parts, fmt.Sprintf("questions %d", m.state.QuestionRequest.Count))
	}
	if m.state.SelectionRequest != nil {
		parts = append(parts, fmt.Sprintf("%s options %d", m.state.SelectionRequest.Kind, m.state.SelectionRequest.Count))
	}
	return strings.Join(parts, " | ")
}
