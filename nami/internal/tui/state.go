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
	MemoryCount        int
	RetrievalSummary   string
	Compacting         bool
	CompactSummary     string
	LastTiming         string
	SessionID          string
	SessionTitle       string
	Artifacts          map[string]artifactState
	FocusedArtifactID  string
	ArtifactReview     *artifactReviewState
	BackgroundCommands map[string]backgroundCommandState
	BackgroundAgents   map[string]backgroundAgentState
	PermissionRequest  *permissionRequestState
	QuestionRequest    *questionRequestState
	SelectionRequest   *selectionRequestState
	Hydrated           bool
	Transcript         []transcriptEntry
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

type permissionRequestState struct {
	RequestID string
	Tool      string
	Risk      string
	Command   string
}

type questionRequestState struct {
	RequestID string
	Count     int
}

type selectionRequestState struct {
	Kind      string
	RequestID string
	Title     string
	Count     int
}

func newUIState() uiState {
	return uiState{
		Status:             "starting",
		Artifacts:          make(map[string]artifactState),
		BackgroundCommands: make(map[string]backgroundCommandState),
		BackgroundAgents:   make(map[string]backgroundAgentState),
		Transcript:         []transcriptEntry{{Kind: "system", Text: "Nami Bubble Tea shell starting..."}},
	}
}

func (s uiState) appendLine(line string) uiState {
	return s.appendTranscript("system", line)
}

func (s uiState) appendTranscript(kind, text string) uiState {
	s.Transcript = append(s.Transcript, transcriptEntry{Kind: kind, Text: text})
	return s
}

func (s uiState) startTurn(prompt string) uiState {
	s = s.appendTranscript("user", prompt)
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
		return s.appendTranscript("error", "engine stopped: "+err.Error())
	}
	return s.appendLine("engine stopped")
}
