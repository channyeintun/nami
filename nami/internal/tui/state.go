package tui

type uiState struct {
	Ready        bool
	Status       string
	Mode         string
	Model        string
	Reasoning    string
	ContextUsage int
	ContextMax   int
	TotalUSD     float64
	InputTokens  int
	OutputTokens int
	RateLimit    string
	Lines        []string
	ErrorMessage string
	TurnActive   bool
	Assistant    string
}

func newUIState() uiState {
	return uiState{
		Status: "starting",
		Lines:  []string{"Nami Bubble Tea shell starting..."},
	}
}

func (s uiState) appendLine(line string) uiState {
	s.Lines = append(s.Lines, line)
	return s
}

func (s uiState) startTurn(prompt string) uiState {
	s = s.appendLine("> " + prompt)
	s.Assistant = ""
	s.TurnActive = true
	s.Status = "running"
	return s
}

func (s uiState) stopEngine(err error) uiState {
	s.TurnActive = false
	s.Status = "stopped"
	if err != nil {
		s.ErrorMessage = err.Error()
		return s.appendLine("engine stopped: " + err.Error())
	}
	return s.appendLine("engine stopped")
}
