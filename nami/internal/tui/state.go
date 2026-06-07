package tui

type uiState struct {
	Ready              bool
	Status             string
	Mode               string
	Model              string
	Reasoning          string
	ContextUsage       int
	ContextMax         int
	TotalUSD           float64
	InputTokens        int
	OutputTokens       int
	RateLimit          string
	Artifacts          map[string]artifactState
	FocusedArtifactID  string
	ArtifactReview     *artifactReviewState
	BackgroundCommands map[string]backgroundCommandState
	BackgroundAgents   map[string]backgroundAgentState
	Lines              []string
	ErrorMessage       string
	TurnActive         bool
	Assistant          string
}

type artifactState struct {
	ID      string
	Kind    string
	Title   string
	Version int
	Status  string
}

type artifactReviewState struct {
	RequestID string
	Artifact  artifactState
}

type backgroundCommandState struct {
	ID          string
	Command     string
	Status      string
	Running     bool
	Error       string
	UnreadBytes int
}

type backgroundAgentState struct {
	ID          string
	Description string
	Status      string
	Error       string
	TotalUSD    float64
}

func newUIState() uiState {
	return uiState{
		Status:             "starting",
		Artifacts:          make(map[string]artifactState),
		BackgroundCommands: make(map[string]backgroundCommandState),
		BackgroundAgents:   make(map[string]backgroundAgentState),
		Lines:              []string{"Nami Bubble Tea shell starting..."},
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
